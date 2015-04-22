// hdcache
package torrent

import (
	"encoding/hex"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

//This provider creates an HD cache for each torrent.
//Each time a cache is created or closed, all cache
//are recalculated so they total <= capacity (in MiB).
type HdCacheProvider struct {
	capacity int
	caches   map[string]*HdCache
}

func NewHdCacheProvider(capacity int) CacheProvider {
	os.Mkdir(filepath.FromSlash(os.TempDir()+"/taipeitorrent"), 0777)
	rc := &HdCacheProvider{capacity, make(map[string]*HdCache)}
	return rc
}

func (r *HdCacheProvider) NewCache(infohash string, numPieces int, pieceSize int, torrentLength int64) TorrentCache {
	i := uint32(1)
	rc := &HdCache{pieceSize: pieceSize, atimes: make([]time.Time, numPieces), boxExists: *NewBitset(numPieces),
		isBoxFull: *NewBitset(numPieces), isBoxCommit: *NewBitset(numPieces), isByteSet: make([]Bitset, numPieces),
		boxPrefix:     filepath.FromSlash(os.TempDir() + "/taipeitorrent/" + hex.EncodeToString([]byte(infohash)) + "-"),
		torrentLength: torrentLength, cacheProvider: r, capacity: &i, infohash: infohash}
	rc.empty() //clear out any detritus from previous runs
	r.caches[infohash] = rc
	r.rebalance(true)
	return rc
}

//Rebalance the cache capacity allocations; has to be called on each cache creation or deletion.
//'shouldTrim', if true, causes trimCommitted() to be called on all the caches. Recommended if a new cache was created
//because otherwise the old caches would stay over the new capacity until their next WriteAt happens.
func (r *HdCacheProvider) rebalance(shouldTrim bool) {
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

func (r *HdCacheProvider) cacheClosed(infohash string) {
	delete(r.caches, infohash)
	r.rebalance(false)
}

//'pieceSize' is the size of the average piece
//'capacity' is how many pieces the cache can hold
//'actualUsage' is how many pieces the cache has at the moment
//'atime' is an array of access times for each stored box
//'boxExists' indicates if a box is existent in cache
//'isBoxFull' indicates if a box entirely contains written data
//'isBoxCommit' indicates if a box has been committed to storage
//'isByteSet' for [i] indicates for box 'i' if a byte has been written to
//'boxPrefix' is the partial path to the boxes.
//'torrentLength' is the number of bytes in the torrent
//'cacheProvider' is a pointer to the cacheProvider that created this cache
//'infohash' is the infohash of the torrent
type HdCache struct {
	pieceSize     int
	capacity      *uint32 //Access only through getter/setter
	actualUsage   int
	atimes        []time.Time
	boxExists     Bitset
	isBoxFull     Bitset
	isBoxCommit   Bitset
	isByteSet     []Bitset
	boxPrefix     string
	torrentLength int64
	cacheProvider *HdCacheProvider
	infohash      string
}

func (r *HdCache) Close() {
	r.cacheProvider.cacheClosed(r.infohash)
	r.empty()
}

func (r *HdCache) empty() {
	for i := 0; i < r.boxExists.Len(); i++ {
		os.Remove(r.boxPrefix + strconv.Itoa(i))
	}
}

func (r *HdCache) ReadAt(p []byte, off int64) []Chunk {
	unfulfilled := make([]Chunk, 0)

	boxI := int(off / int64(r.pieceSize))
	boxOff := int(off % int64(r.pieceSize))

	for i := 0; i < len(p); {
		if !r.boxExists.IsSet(boxI) { //definitely not in cache
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
			box, err := os.Open(r.boxPrefix + strconv.Itoa(boxI))
			if err != nil {
				log.Println("Error opening cache item we thought we had:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}

			copied, err := box.ReadAt(p[i:], int64(boxOff))
			box.Close()
			i += copied

			if err != nil && err != io.EOF {
				log.Println("Error while reading cache item:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}
		} else { //Bah, do it byte by byte.
			missing := []*inttuple{&inttuple{-1, -1}}
			end := len(p[i:]) + boxOff
			if end > r.pieceSize {
				end = r.pieceSize
			}

			box, err := ioutil.ReadFile(r.boxPrefix + strconv.Itoa(boxI))
			if err != nil {
				log.Println("Error reading cache item we thought we had:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}

			for j := boxOff; j < end; j++ {
				if r.isByteSet[boxI].IsSet(j) {
					p[i] = box[j]
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

func (r *HdCache) WriteAt(p []byte, off int64) []Chunk {
	boxI := int(off / int64(r.pieceSize))
	boxOff := int(off % int64(r.pieceSize))

	for i := 0; i < len(p); {
		var box *os.File
		var err error
		if !r.boxExists.IsSet(boxI) { //box doesn't exist, so we'll make one.
			box, err = os.Create(r.boxPrefix + strconv.Itoa(boxI))
			if err != nil {
				log.Panicln("Couldn't create cache file:", err)
				return nil
			}
			r.boxExists.Set(boxI)
			box.Truncate(int64(r.pieceSize))
			r.actualUsage++
		} else { //box exists, so we'll open it
			box, err = os.OpenFile(r.boxPrefix+strconv.Itoa(boxI), os.O_WRONLY, 0777)
			if err != nil {
				log.Println("Error opening cache item we thought we had:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}
		}
		end := r.pieceSize - boxOff
		if len(p) < end {
			end = len(p)
		}
		copied, err := box.WriteAt(p[i:end], int64(boxOff))
		if err != nil {
			log.Panicln("Error at write cache box:", box.Name(), "error:", err)
		}
		i += copied
		box.Close()
		r.atimes[boxI] = time.Now()
		if copied == r.pieceSize {
			r.isBoxFull.Set(boxI)
		} else {
			if r.isByteSet[boxI].n == 0 {
				r.isByteSet[boxI] = *NewBitset(r.pieceSize)
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

func (r *HdCache) MarkCommitted(piece int) {
	if r.boxExists.IsSet(piece) {
		r.isBoxFull.Set(piece)
		r.isBoxCommit.Set(piece)
		r.isByteSet[piece] = *NewBitset(0)
	}
}

func (r *HdCache) removeBox(boxI int) {
	r.isBoxFull.Clear(boxI)
	r.boxExists.Clear(boxI)
	r.isBoxCommit.Clear(boxI)
	r.isByteSet[boxI] = *NewBitset(0)
	err := os.Remove(r.boxPrefix + strconv.Itoa(boxI))
	if err != nil {
		log.Println("Error removing cache box:", err)
	} else {
		r.actualUsage--
	}
}

func (r *HdCache) getCapacity() int {
	return int(atomic.LoadUint32(r.capacity))
}

func (r *HdCache) setCapacity(capacity int) {
	atomic.StoreUint32(r.capacity, uint32(capacity))
}

//Trim stuff that's already been committed
//Return true if we got underneath capacity, false if not.
func (r *HdCache) trimCommitted() bool {
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

//Trim excess data. Returns any uncommitted Chunks that were trimmed
func (r *HdCache) trim() []Chunk {

	if r.trimCommitted() {
		return nil
	}

	retVal := make([]Chunk, 0)

	//Still need more space? figure out what's oldest
	//RawWrite it to storage, and clear that then
	tATA := make([]accessTime, 0, r.actualUsage)

	for i, atime := range r.atimes {
		if r.boxExists.IsSet(i) {
			tATA = append(tATA, accessTime{i, atime})
		}
	}

	sort.Sort(byTime(tATA))

	deficit := r.actualUsage - r.getCapacity()
	for i := 0; i < deficit; i++ {
		deadBox := tATA[i].index
		data, err := ioutil.ReadFile(r.boxPrefix + strconv.Itoa(deadBox))
		if err != nil {
			log.Println("Error reading cache box for trimming:", err)
		} else {
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
		}
		r.removeBox(deadBox)
	}
	return retVal
}
