// cache
package storage

import (
	"log"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/jackpal/Taipei-Torrent/bitset"
)

type CacheProvider interface {
	NewCache(infohash string, numPieces int, pieceLength int, totalSize int64) TorrentCache
}

type Chunk struct {
	I    int64
	Data []byte
}

type TorrentCache interface {
	//Read what's cached, returns parts that weren't available to read.
	ReadAt(p []byte, offset int64) []Chunk
	//Writes to cache, returns uncommitted data that has been trimmed.
	WriteAt(p []byte, offset int64) []Chunk
	//Marks a piece as committed to permanent storage.
	MarkCommitted(piece int)
	//Close the cache and free all the things
	Close()
}

type inttuple struct {
	a, b int
}

type accessTime struct {
	index int
	atime time.Time
}
type byTime []accessTime

func (a byTime) Len() int           { return len(a) }
func (a byTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byTime) Less(i, j int) bool { return a[i].atime.Before(a[j].atime) }

//This provider creates a ram cache for each torrent.
//Each time a cache is created or closed, all cache
//are recalculated so they total <= capacity (in MiB).
type RamCacheProvider struct {
	capacity int
	caches   map[string]*RamCache
}

func NewRamCacheProvider(capacity int) CacheProvider {
	rc := &RamCacheProvider{capacity, make(map[string]*RamCache)}
	return rc
}

func (r *RamCacheProvider) NewCache(infohash string, numPieces int, pieceSize int, torrentLength int64) TorrentCache {
	i := uint32(1)
	rc := &RamCache{
		pieceSize:     pieceSize,
		atimes:        make([]time.Time, numPieces),
		store:         make([][]byte, numPieces),
		isBoxFull:     bitset.New(numPieces),
		isBoxCommit:   bitset.New(numPieces),
		isByteSet:     make([]*bitset.Bitset, numPieces),
		torrentLength: torrentLength,
		cacheProvider: r,
		capacity:      &i,
		infohash:      infohash,
	}

	r.caches[infohash] = rc
	r.rebalance(true)
	return rc
}

//Rebalance the cache capacity allocations; has to be called on each cache creation or deletion.
//'shouldTrim', if true, causes trimCommitted() to be called on all the caches. Recommended if a new cache was created
//because otherwise the old caches would stay over the new capacity until their next WriteAt happens.
func (r *RamCacheProvider) rebalance(shouldTrim bool) {
	//Cache size is a diminishing return thing:
	//The more of it a torrent has, the less of a difference additional cache makes.
	//Thus, instead of scaling the distribution lineraly with torrent size, we'll do it by square-root
	log.Println("Rebalancing caches...")
	var scalingTotal float64
	sqrts := make(map[string]float64)
	for i, cache := range r.caches {
		sqrts[i] = math.Sqrt(float64(cache.torrentLength))
		scalingTotal += sqrts[i]
	}

	scalingFactor := float64(r.capacity*1024*1024) / scalingTotal
	for i, cache := range r.caches {
		newCap := int(math.Floor(scalingFactor * sqrts[i] / float64(cache.pieceSize)))
		if newCap == 0 {
			newCap = 1 //Something's better than nothing!
		}
		log.Printf("Setting cache '%x' to new capacity %v (%v MiB)", cache.infohash, newCap, float32(newCap*cache.pieceSize)/float32(1024*1024))
		cache.setCapacity(newCap)
	}

	if shouldTrim {
		for _, cache := range r.caches {
			cache.trimCommitted()
		}
	}
}

func (r *RamCacheProvider) cacheClosed(infohash string) {
	delete(r.caches, infohash)
	r.rebalance(false)
}

type RamCache struct {
	// How many pieces the cache can hold
	capacity *uint32 //Access only through getter/setter

	// How many pieces the cache has at the moment
	actualUsage int

	// Access times for each stored box
	atimes []time.Time

	// Bitset of boxes that entirely contain written data
	isBoxFull *bitset.Bitset

	// Bitset of boxes committed to storage
	isBoxCommit *bitset.Bitset

	// Slice of written bytes
	isByteSet []*bitset.Bitset

	// Array of boxes, where a box is a []byte for 1 piece
	store [][]byte

	pieceSize     int
	torrentLength int64
	// Pointer to the CacheProvider that created this cache
	cacheProvider *RamCacheProvider
	infohash      string
}

func (r *RamCache) Close() {
	r.cacheProvider.cacheClosed(r.infohash)
	//We don't need to do anything else. The garbage collector will take care of it.
}

func (r *RamCache) ReadAt(p []byte, off int64) []Chunk {
	unfulfilled := make([]Chunk, 0)

	boxI := int(off / int64(r.pieceSize))
	boxOff := int(off % int64(r.pieceSize))

	for i := 0; i < len(p); {
		if r.store[boxI] == nil { //definitely not in cache
			end := len(p[i:])
			if end > r.pieceSize-boxOff {
				end = r.pieceSize - boxOff
			}
			if len(unfulfilled) > 0 {
				last := unfulfilled[len(unfulfilled)-1]
				if last.I+int64(len(last.Data)) == off+int64(i) {
					unfulfilled = unfulfilled[:len(unfulfilled)-1]
					i = int(last.I - off)
					end += len(last.Data)
				}
			}
			unfulfilled = append(unfulfilled, Chunk{off + int64(i), p[i : i+end]})
			i += end
		} else if r.isBoxFull.IsSet(boxI) { //definitely in cache
			i += copy(p[i:], r.store[boxI][boxOff:])
		} else { //Bah, do it byte by byte.
			missing := []*inttuple{&inttuple{-1, -1}}
			end := len(p[i:]) + boxOff
			if end > r.pieceSize {
				end = r.pieceSize
			}
			for j := boxOff; j < end; j++ {
				if r.isByteSet[boxI].IsSet(j) {
					p[i] = r.store[boxI][j]
				} else {
					lastIT := missing[len(missing)-1]
					if lastIT.b == i {
						lastIT.b = i + 1
					} else {
						missing = append(missing, &inttuple{i, i + 1})
					}
				}
				i++
			}
			for _, intt := range missing[1:] {
				unfulfilled = append(unfulfilled, Chunk{off + int64(intt.a), p[intt.a:intt.b]})
			}
		}
		boxI++
		boxOff = 0
	}
	return unfulfilled
}

func (r *RamCache) WriteAt(p []byte, off int64) []Chunk {
	boxI := int(off / int64(r.pieceSize))
	boxOff := int(off % int64(r.pieceSize))

	for i := 0; i < len(p); {
		if r.store[boxI] == nil {
			r.store[boxI] = make([]byte, r.pieceSize)
			r.actualUsage++
		}
		copied := copy(r.store[boxI][boxOff:], p[i:])
		i += copied
		r.atimes[boxI] = time.Now()
		if copied == r.pieceSize {
			r.isBoxFull.Set(boxI)
		} else {
			if r.isByteSet[boxI] == nil {
				r.isByteSet[boxI] = bitset.New(r.pieceSize)
			}
			for j := boxOff; j < boxOff+copied; j++ {
				r.isByteSet[boxI].Set(j)
			}
		}
		boxI++
		boxOff = 0
	}
	if r.actualUsage > r.getCapacity() {
		return r.trim()
	}
	return nil
}

func (r *RamCache) MarkCommitted(piece int) {
	if r.store[piece] != nil {
		r.isBoxFull.Set(piece)
		r.isBoxCommit.Set(piece)
		r.isByteSet[piece] = bitset.New(0)
	}
}

func (r *RamCache) removeBox(boxI int) {
	r.isBoxFull.Clear(boxI)
	r.isBoxCommit.Clear(boxI)
	r.isByteSet[boxI] = bitset.New(0)
	r.store[boxI] = nil
	r.actualUsage--
}

func (r *RamCache) getCapacity() int {
	return int(atomic.LoadUint32(r.capacity))
}

func (r *RamCache) setCapacity(capacity int) {
	atomic.StoreUint32(r.capacity, uint32(capacity))
}

//Trim stuff that's already been committed
//Return true if we got underneath capacity, false if not.
func (r *RamCache) trimCommitted() bool {
	for i := 0; i < r.isBoxCommit.Len(); i++ {
		if r.isBoxCommit.IsSet(i) {
			r.removeBox(i)
		}
		if r.actualUsage <= r.getCapacity() {
			return true
		}
	}
	return false
}

//Trim excess data. Returns any uncommitted chunks that were trimmed
func (r *RamCache) trim() []Chunk {

	if r.trimCommitted() {
		return nil
	}

	retVal := make([]Chunk, 0)

	//Still need more space? figure out what's oldest
	//RawWrite it to storage, and clear that then
	tATA := make([]accessTime, 0, r.actualUsage)

	for i, atime := range r.atimes {
		if r.store[i] != nil {
			tATA = append(tATA, accessTime{i, atime})
		}
	}

	sort.Sort(byTime(tATA))

	deficit := r.actualUsage - r.getCapacity()
	for i := 0; i < deficit; i++ {
		deadBox := tATA[i].index
		data := r.store[deadBox]
		if r.isBoxFull.IsSet(deadBox) { //Easy, the whole box has to go
			retVal = append(retVal, Chunk{int64(deadBox) * int64(r.pieceSize), data})
		} else { //Ugh, we'll just trim anything unused from the start and the end, and send that.
			off := int64(0)
			endData := r.pieceSize
			//Trim out any unset bytes at the beginning
			for j := 0; j < r.pieceSize; j++ {
				if !r.isByteSet[deadBox].IsSet(j) {
					off++
				} else {
					break
				}
			}

			//Trim out any unset bytes at the end
			for j := r.pieceSize - 1; j > 0; j-- {
				if !r.isByteSet[deadBox].IsSet(j) {
					endData--
				} else {
					break
				}
			}
			retVal = append(retVal, Chunk{int64(deadBox)*int64(r.pieceSize) + off, data[off:endData]})
		}
		r.removeBox(deadBox)
	}
	return retVal
}

//Simple utility for dumping a []byte to log.
//It skips over sections of '0', unlike encoding/hex.Dump()
func Dump(buff []byte) {
	log.Println("Dumping []byte len=", len(buff))
	for i := 0; i < len(buff); i += 16 {
		skipLine := true
		for j := i; j < len(buff) && j < 16+i; j++ {
			if buff[j] != 0 {
				skipLine = false
				break
			}
		}
		if !skipLine {
			log.Printf("%X: %X\n", i, buff[i:i+16])
		}
	}
	log.Println("Done Dumping")
}
