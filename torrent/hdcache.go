// hdcache
package torrent

import (
	"encoding/hex"
	"io"
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

func (r *HdCacheProvider) NewCache(infohash string, numPieces int, pieceSize int64, torrentLength int64, underlying FileStore) FileStore {
	i := uint32(1)
	rc := &HdCache{pieceSize: pieceSize, atimes: make([]time.Time, numPieces), boxExists: *NewBitset(numPieces),
		boxPrefix:     filepath.FromSlash(os.TempDir() + "/taipeitorrent/" + hex.EncodeToString([]byte(infohash)) + "-"),
		torrentLength: torrentLength, cacheProvider: r, capacity: &i, infohash: infohash, underlying: underlying}
	rc.empty() //clear out any detritus from previous runs
	r.caches[infohash] = rc
	r.rebalance()
	return rc
}

//Rebalance the cache capacity allocations; has to be called on each cache creation or deletion.
func (r *HdCacheProvider) rebalance() {
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

func (r *HdCacheProvider) cacheClosed(infohash string) {
	delete(r.caches, infohash)
	r.rebalance()
}

//'pieceSize' is the size of the average piece
//'capacity' is how many pieces the cache can hold
//'actualUsage' is how many pieces the cache has at the moment
//'atime' is an array of access times for each stored box
//'boxExists' indicates if a box is existent in cache
//'boxPrefix' is the partial path to the boxes.
//'torrentLength' is the number of bytes in the torrent
//'cacheProvider' is a pointer to the cacheProvider that created this cache
//'infohash' is the infohash of the torrent
//'underlying' is the FileStore we're caching
type HdCache struct {
	pieceSize     int64
	capacity      *uint32 //Access only through getter/setter
	actualUsage   int
	atimes        []time.Time
	boxExists     Bitset
	boxPrefix     string
	torrentLength int64
	cacheProvider *HdCacheProvider
	infohash      string
	underlying    FileStore
}

func (r *HdCache) Close() error {
	r.cacheProvider.cacheClosed(r.infohash)
	r.empty()
	return r.underlying.Close()
}

func (r *HdCache) empty() {
	for i := 0; i < r.boxExists.Len(); i++ {
		os.Remove(r.boxPrefix + strconv.Itoa(i))
	}
}

func (r *HdCache) ReadAt(p []byte, off int64) (retInt int, retErr error) {
	boxI := int(off / r.pieceSize)
	boxOff := off % r.pieceSize

	for i := 0; i < len(p); {
		copied := 0
		if !r.boxExists.IsSet(boxI) { //not in cache
			bufferLength := r.pieceSize
			bufferOffset := int64(boxI) * r.pieceSize

			if bufferLength > r.torrentLength-bufferOffset { //do we want the last, smaller than usual piece?
				bufferLength = r.torrentLength - bufferOffset
			}

			buffer := make([]byte, bufferLength)
			r.underlying.ReadAt(buffer, bufferOffset)
			copied = copy(p[i:], buffer[boxOff:])
			r.addBox(buffer, boxI)

		} else { //in cache
			box, err := os.Open(r.boxPrefix + strconv.Itoa(boxI))
			if err != nil {
				log.Println("Error opening cache item we thought we had:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				box.Close()
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}

			copied, err = box.ReadAt(p[i:], boxOff)
			box.Close()
			r.atimes[boxI] = time.Now()

			if err != nil && err != io.EOF {
				log.Println("Error while reading cache item:", r.boxPrefix+strconv.Itoa(boxI), "error:", err)
				r.removeBox(boxI)
				continue //loop around without incrementing 'i', forget this ever happened
			}
		}

		i += copied
		boxI++
		boxOff = 0
	}

	retInt = len(p)
	return
}

func (r *HdCache) WritePiece(p []byte, boxI int) (n int, retErr error) {

	if r.boxExists.IsSet(boxI) { //box exists, our work is done
		log.Println("Got a WritePiece for a piece we should already have:", boxI)
		return
	}

	r.addBox(p, boxI)

	//TODO: Maybe goroutine the calls to underlying?
	return r.underlying.WritePiece(p, boxI)
}

func (r *HdCache) addBox(p []byte, boxI int) {

	box, err := os.Create(r.boxPrefix + strconv.Itoa(boxI))
	if err != nil {
		log.Println("Couldn't create cache file:", err)
	} else {
		box.Truncate(int64(len(p)))
		r.actualUsage++
		//TODO: Maybe goroutine the calls to box?
		_, err = box.WriteAt(p, 0)
		if err != nil {
			log.Println("Error at write cache box:", box.Name(), "error:", err)
		} else {
			r.atimes[boxI] = time.Now()
			r.boxExists.Set(boxI)
		}
		box.Close()
	}

	r.trim()
}

func (r *HdCache) removeBox(boxI int) {
	r.boxExists.Clear(boxI)
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

func (r *HdCache) setCapacity(capacity uint32) {
	atomic.StoreUint32(r.capacity, capacity)
}

//Trim excess data.
func (r *HdCache) trim() {
	if r.actualUsage <= r.getCapacity() {
		return
	}

	//Figure out what's oldest and clear that then
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
		r.removeBox(deadBox)
	}
}
