// cache
package torrent

import (
	"log"
	"sort"
	"time"
)

type CacheProvider interface {
	NewCache(infohash string, capacity int64) TorrentCache
}

const RAMCHUNKS = 1024 * 1024

type TorrentCache interface {
	//Read what's cached, returns parts that weren't available to read.
	ReadAt(p []byte, off int64) []chunk
	//Writes to cache. No guarantee that it'll be here later.
	WriteAt(p []byte, off int64)
}

//This simple provider creates a ram cache for each torrent.
//Each cache has size capacity (in MiB).
type RamCacheProvider struct {
	capacity int
}

func NewRamCacheProvider(capacity int) CacheProvider {
	rc := &RamCacheProvider{capacity}
	return rc
}

func (r *RamCacheProvider) NewCache(infohash string, torrentLength int64) TorrentCache {
	mib := int64(RAMCHUNKS)
	boxes := int(torrentLength / mib)
	if torrentLength%mib > 0 {
		boxes++
	}
	rc := &RamCache{r.capacity, 0, torrentLength, make([][]byte, boxes), make([]time.Time, boxes), *NewBitset(boxes), make([]Bitset, boxes)}
	return rc
}

//'maxCapacity' is how large in MiB the cache can grow
//'actualUsage' is how large in MiB the cache is at the moment
//'torrentLength' is the size in bytes of the whole torrent
//'store' is an array of "boxes" ([]byte of 1 MiB each)
//'atime' is an array of access times for each stored box
//'isSetBitSet
type RamCache struct {
	maxCapacity   int
	actualUsage   int
	torrentLength int64
	store         [][]byte
	atimes        []time.Time
	isBoxFull     Bitset
	isByteSet     []Bitset
}

type int64arr []int64

func (a int64arr) Len() int           { return len(a) }
func (a int64arr) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a int64arr) Less(i, j int) bool { return a[i] < a[j] }

func (r *RamCache) ReadAt(p []byte, off int64) []chunk {
	unfulfilled := make([]chunk, 0)

	boxI := int(off / int64(RAMCHUNKS))
	boxOff := int(off % int64(RAMCHUNKS))

	for i := 0; i < len(p); {
		if r.store[boxI] == nil { //definitely not in cache
			end := len(p[i:])
			if end > RAMCHUNKS-boxOff {
				end = RAMCHUNKS - boxOff
			}
			if len(unfulfilled) > 0 {
				last := unfulfilled[len(unfulfilled)-1]
				if last.i+int64(len(last.data)) == off+int64(i) {
					unfulfilled = unfulfilled[:len(unfulfilled)-1]
					i = int(last.i - off)
					end += len(last.data)
				}
			}
			unfulfilled = append(unfulfilled, chunk{off + int64(i), p[i : i+end]})
			i += end
		} else if r.isBoxFull.IsSet(boxI) { //definitely in cache
			i += copy(p[i:], r.store[boxI][boxOff:])
		} else { //Bah, do it byte by byte.
			missing := []*inttuple{&inttuple{-1, -1}}
			end := len(p[i:]) + boxOff
			if end > RAMCHUNKS {
				end = RAMCHUNKS
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
				unfulfilled = append(unfulfilled, chunk{off + int64(intt.a), p[intt.a:intt.b]})
			}
		}
		boxI++
		boxOff = 0
	}
	return unfulfilled
}

type inttuple struct {
	a, b int
}

func (r *RamCache) WriteAt(p []byte, off int64) {
	boxI := int(off / int64(RAMCHUNKS))
	boxOff := int(off % int64(RAMCHUNKS))

	for i := 0; i < len(p); {
		if r.store[boxI] == nil {
			r.store[boxI] = make([]byte, RAMCHUNKS)
			r.actualUsage++
		}
		copied := copy(r.store[boxI][boxOff:], p[i:])
		r.atimes[boxI] = time.Now()
		if copied == RAMCHUNKS {
			r.isBoxFull.Set(boxI)
		} else {
			if r.isByteSet[boxI].n == 0 {
				r.isByteSet[boxI] = *NewBitset(RAMCHUNKS)
			}
			for j := boxOff; j < boxOff+copied; j++ {
				r.isByteSet[boxI].Set(j)
			}
		}
		i += copied
		boxI++
		boxOff = 0
	}
	if r.actualUsage > r.maxCapacity {
		r.trim()
	}
}

type accessTime struct {
	index int
	atime time.Time
}
type byTime []accessTime

func (a byTime) Len() int           { return len(a) }
func (a byTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byTime) Less(i, j int) bool { return a[i].atime.Before(a[j].atime) }

//Trim excess data
func (r *RamCache) trim() {
	tATA := make([]accessTime, 0, r.actualUsage)

	for i, atime := range r.atimes {
		if r.store[i] != nil {
			tATA = append(tATA, accessTime{i, atime})
		}
	}

	sort.Sort(byTime(tATA))

	deficit := r.actualUsage - r.maxCapacity
	for i := 0; i < deficit; i++ {
		deadBox := tATA[i].index
		r.store[deadBox] = nil
		r.isBoxFull.Clear(deadBox)
		r.isByteSet[deadBox] = *NewBitset(0)
		r.actualUsage--
	}
}

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
