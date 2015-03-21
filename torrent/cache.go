// cache
package torrent

import (
	"log"
	"sort"
	"time"
)

type CacheProvider interface {
	NewCache(infohash string, numPieces int, pieceLength int, totalSize int64) TorrentCache
}

type TorrentCache interface {
	//Read what's cached, returns parts that weren't available to read.
	ReadAt(p []byte, offset int64) []chunk
	//Writes to cache, returns uncommitted data that has been trimmed.
	WriteAt(p []byte, offset int64) []chunk
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

//This simple provider creates a ram cache for each torrent.
//Each cache has size capacity (in MiB).
type RamCacheProvider struct {
	capacity int
}

func NewRamCacheProvider(capacity int) CacheProvider {
	rc := &RamCacheProvider{capacity}
	return rc
}

func (r *RamCacheProvider) NewCache(infohash string, numPieces int, pieceSize int, torrentLength int64) TorrentCache {
	maxNumPieces := (int64(r.capacity) * 1024 * 1024) / int64(pieceSize)
	rc := &RamCache{pieceSize, int(maxNumPieces), 0, make([]time.Time, numPieces),
		make([][]byte, numPieces), *NewBitset(numPieces), *NewBitset(numPieces), make([]Bitset, numPieces)}
	return rc
}

//'pieceSize' is the size of the average piece
//'maxCapacity' is how many pieces the cache can hold
//'actualUsage' is how many pieces the cache has at the moment
//'atime' is an array of access times for each stored box
//'store' is an array of "boxes" ([]byte of 1 piece each)
//'isBoxFull' indicates if a box entirely contains written data
//'isBoxCommit' indicates if a box has been committed to storage
//'isByteSet' for [i] indicates for box 'i' if a byte has been written to
type RamCache struct {
	pieceSize   int
	maxCapacity   int
	actualUsage   int
	atimes      []time.Time
	store         [][]byte
	isBoxFull     Bitset
	isBoxCommit Bitset
	isByteSet     []Bitset
}

func (r *RamCache) Close() {
}

func (r *RamCache) ReadAt(p []byte, off int64) []chunk {
	unfulfilled := make([]chunk, 0)

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
				unfulfilled = append(unfulfilled, chunk{off + int64(intt.a), p[intt.a:intt.b]})
			}
		}
		boxI++
		boxOff = 0
	}
	return unfulfilled
}

func (r *RamCache) WriteAt(p []byte, off int64) []chunk {
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
	if r.actualUsage > r.maxCapacity {
		return r.trim()
	}
	return nil
}

func (r *RamCache) MarkCommitted(piece int) {
	if r.store[piece] != nil {
		r.isBoxFull.Set(piece)
		r.isBoxCommit.Set(piece)
		r.isByteSet[piece] = *NewBitset(0)
	}
}

func (r *RamCache) removeBox(boxI int) {
	r.isBoxFull.Clear(boxI)
	r.isBoxCommit.Clear(boxI)
	r.isByteSet[boxI] = *NewBitset(0)
	r.store[boxI] = nil
	r.actualUsage--
}

//Trim excess data. Returns any uncommitted chunks that were trimmed
func (r *RamCache) trim() []chunk {
	//Trim stuff that's already been committed first
	for i := 0; i < r.isBoxCommit.Len(); i++ {
		if r.isBoxCommit.IsSet(i) {
			r.removeBox(i)
		}
		if r.actualUsage == r.maxCapacity {
			return nil
		}
}

	retVal := make([]chunk, 0)

	//Still need more space? figure out what's oldest
	//RawWrite it to storage, and clear that then
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
		data := r.store[deadBox]
		if r.isBoxFull.IsSet(deadBox) { //Easy, the whole box has to go
			retVal = append(retVal, chunk{int64(deadBox) * int64(r.pieceSize), data})
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
			retVal = append(retVal, chunk{int64(deadBox)*int64(r.pieceSize) + off, data[off:endData]})
		}
		r.removeBox(deadBox)
	}
	return retVal
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
