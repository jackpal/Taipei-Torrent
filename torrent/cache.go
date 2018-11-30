// cache
package torrent

import (
	"log"
	"math"
	"sort"
	"sync/atomic"
	"time"
)

type CacheProvider interface {
	NewCache(infohash string, numPieces int, pieceLength int64, totalSize int64, undelying FileStore) FileStore
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

func (r *RamCacheProvider) NewCache(infohash string, numPieces int, pieceSize int64, torrentLength int64, underlying FileStore) FileStore {
	i := uint32(1)
	rc := &RamCache{pieceSize: pieceSize, atimes: make([]time.Time, numPieces), store: make([][]byte, numPieces),
		torrentLength: torrentLength, cacheProvider: r, capacity: &i, infohash: infohash, underlying: underlying}

	r.caches[infohash] = rc
	r.rebalance()
	return rc
}

//Rebalance the cache capacity allocations; has to be called on each cache creation or deletion.
func (r *RamCacheProvider) rebalance() {
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
		newCap := int64(math.Floor(scalingFactor * sqrts[i] / float64(cache.pieceSize)))
		if newCap == 0 {
			newCap = 1 //Something's better than nothing!
		}
		log.Printf("Setting cache '%x' to new capacity %v (%v MiB)", cache.infohash, newCap, float32(newCap*cache.pieceSize)/float32(1024*1024))
		cache.setCapacity(uint32(newCap))
	}

	for _, cache := range r.caches {
		cache.trim()
	}
}

func (r *RamCacheProvider) cacheClosed(infohash string) {
	delete(r.caches, infohash)
	r.rebalance()
}

//'pieceSize' is the size of the average piece
//'capacity' is how many pieces the cache can hold
//'actualUsage' is how many pieces the cache has at the moment
//'atime' is an array of access times for each stored box
//'store' is an array of "boxes" ([]byte of 1 piece each)
//'torrentLength' is the number of bytes in the torrent
//'cacheProvider' is a pointer to the cacheProvider that created this cache
//'infohash' is the infohash of the torrent
type RamCache struct {
	pieceSize     int64
	capacity      *uint32 //Access only through getter/setter
	actualUsage   int
	atimes        []time.Time
	store         [][]byte
	torrentLength int64
	cacheProvider *RamCacheProvider
	infohash      string
	underlying    FileStore
}

func (r *RamCache) Close() error {
	r.cacheProvider.cacheClosed(r.infohash)
	r.store = nil
	return r.underlying.Close()
}

func (r *RamCache) ReadAt(p []byte, off int64) (retInt int, retErr error) {
	boxI := off / r.pieceSize
	boxOff := off % r.pieceSize

	for i := 0; i < len(p); {

		var buffer []byte
		if r.store[boxI] != nil { //in cache
			buffer = r.store[boxI]
			r.atimes[boxI] = time.Now()
		} else { //not in cache
			bufferLength := r.pieceSize
			bufferOffset := boxI * r.pieceSize

			if bufferLength > r.torrentLength-bufferOffset { //do we want the last, smaller than usual piece?
				bufferLength = r.torrentLength - bufferOffset
			}

			buffer = make([]byte, bufferLength)
			r.underlying.ReadAt(buffer, bufferOffset)
			r.addBox(buffer, int(boxI))
		}

		i += copy(p[i:], buffer[boxOff:])
		boxI++
		boxOff = 0
	}

	retInt = len(p)
	return
}

func (r *RamCache) WritePiece(p []byte, boxI int) (n int, err error) {

	if r.store[boxI] != nil { //box exists, our work is done
		log.Println("Got a WritePiece for a piece we should already have:", boxI)
		return
	}

	r.addBox(p, boxI)

	//TODO: Maybe goroutine the calls to underlying?
	return r.underlying.WritePiece(p, boxI)
}

func (r *RamCache) addBox(p []byte, boxI int) {
	r.store[boxI] = p
	r.atimes[boxI] = time.Now()
	r.actualUsage++
	r.trim()
}

func (r *RamCache) removeBox(boxI int) {
	r.store[boxI] = nil
	r.actualUsage--
}

func (r *RamCache) getCapacity() int {
	return int(atomic.LoadUint32(r.capacity))
}

func (r *RamCache) setCapacity(capacity uint32) {
	atomic.StoreUint32(r.capacity, capacity)
}

//Trim excess data.
func (r *RamCache) trim() {
	if r.actualUsage <= r.getCapacity() {
		return
	}

	//Figure out what's oldest and clear that then
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
		r.removeBox(deadBox)
	}
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
