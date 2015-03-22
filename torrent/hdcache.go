// hdcache
package torrent

import (
	"encoding/hex"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

//This simple provider creates an HD cache for each torrent.
//Each cache has size capacity (in MiB).
type HdCacheProvider struct {
	capacity int
}

func NewHdCacheProvider(capacity int) CacheProvider {
	os.Mkdir(filepath.FromSlash(os.TempDir()+"/taipeitorrent"), 0777)
	rc := &HdCacheProvider{capacity}
	return rc
}

func (r *HdCacheProvider) NewCache(infohash string, numPieces int, pieceSize int, torrentLength int64) TorrentCache {
	maxNumPieces := (int64(r.capacity) * 1024 * 1024) / int64(pieceSize)
	rc := &HdCache{pieceSize, int(maxNumPieces), 0, make([]time.Time, numPieces), *NewBitset(numPieces),
		*NewBitset(numPieces), *NewBitset(numPieces), make([]Bitset, numPieces),
		filepath.FromSlash(os.TempDir() + "/taipeitorrent/" + hex.EncodeToString([]byte(infohash)) + "-")}
	rc.empty() //clear out any detritus from previous runs
	return rc
}

//'pieceSize' is the size of the average piece
//'maxCapacity' is how many pieces the cache can hold
//'actualUsage' is how many pieces the cache has at the moment
//'atime' is an array of access times for each stored box
//'boxExists' indicates if a box is existent in cache
//'isBoxFull' indicates if a box entirely contains written data
//'isBoxCommit' indicates if a box has been committed to storage
//'isByteSet' for [i] indicates for box 'i' if a byte has been written to
//'boxPrefix' is the partial path to the boxes.
type HdCache struct {
	pieceSize   int
	maxCapacity int
	actualUsage int
	atimes      []time.Time
	boxExists   Bitset
	isBoxFull   Bitset
	isBoxCommit Bitset
	isByteSet   []Bitset
	boxPrefix   string
}

func (r *HdCache) Close() {
	r.empty()
}

func (r *HdCache) empty() {
	for i := 0; i < r.boxExists.Len(); i++ {
		os.Remove(r.boxPrefix + strconv.Itoa(i))
	}
}

func (r *HdCache) ReadAt(p []byte, off int64) []chunk {
	unfulfilled := make([]chunk, 0)

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
				if last.i+int64(len(last.data)) == off+int64(i) {
					unfulfilled = unfulfilled[:len(unfulfilled)-1]
					i = int(last.i - off)
					end += len(last.data)
				}
			}
			unfulfilled = append(unfulfilled, chunk{off + int64(i), p[i : i+end]})
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
				unfulfilled = append(unfulfilled, chunk{off + int64(intt.a), p[intt.a:intt.b]})
			}
		}
		boxI++
		boxOff = 0
	}
	return unfulfilled
}

func (r *HdCache) WriteAt(p []byte, off int64) []chunk {
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
	if r.actualUsage > r.maxCapacity {
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

//Trim excess data. Returns any uncommitted chunks that were trimmed
func (r *HdCache) trim() []chunk {
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
		if r.boxExists.IsSet(i) {
			tATA = append(tATA, accessTime{i, atime})
		}
	}

	sort.Sort(byTime(tATA))

	deficit := r.actualUsage - r.maxCapacity
	for i := 0; i < deficit; i++ {
		deadBox := tATA[i].index
		data, err := ioutil.ReadFile(r.boxPrefix + strconv.Itoa(deadBox))
		if err != nil {
			log.Println("Error reading cache box for trimming:", err)
		} else {
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
		}
		r.removeBox(deadBox)
	}
	return retVal
}
