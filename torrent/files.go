package torrent

import (
	"errors"
	"io"
	"log"

	"github.com/jackpal/Taipei-Torrent/storage"
)

// A torrent file store.
type FileStore interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	SetCache(storage.TorrentCache)
	Commit(int, []byte, int64)
}

type fileStore struct {
	fileSystem storage.FileSystem
	offsets    []int64
	files      []fileEntry // Stored in increasing globalOffset order
	cache      storage.TorrentCache
}

type fileEntry struct {
	length int64
	file   storage.File
}

func (f *fileStore) SetCache(cache storage.TorrentCache) {
	f.cache = cache
}

func (f *fileStore) find(offset int64) int {
	// Binary search
	offsets := f.offsets
	low := 0
	high := len(offsets)
	for low < high-1 {
		probe := (low + high) / 2
		entry := offsets[probe]
		if offset < entry {
			high = probe
		} else {
			low = probe
		}
	}
	return low
}

func (f *fileStore) ReadAt(p []byte, off int64) (int, error) {
	if f.cache == nil {
		return f.RawReadAt(p, off)
	}

	unfullfilled := f.cache.ReadAt(p, off)

	var retErr error
	for _, unf := range unfullfilled {
		_, err := f.RawReadAt(unf.Data, unf.I)
		if err != nil {
			log.Println("Got an error on read (off=", unf.I, "len=", len(unf.Data), ") from filestore:", err)
			retErr = err
		}
	}
	return len(p), retErr
}

func (f *fileStore) RawReadAt(p []byte, off int64) (n int, err error) {
	index := f.find(off)
	for len(p) > 0 && index < len(f.offsets) {
		chunk := int64(len(p))
		entry := &f.files[index]
		itemOffset := off - f.offsets[index]
		if itemOffset < entry.length {
			space := entry.length - itemOffset
			if space < chunk {
				chunk = space
			}
			var nThisTime int
			nThisTime, err = entry.file.ReadAt(p[0:chunk], itemOffset)
			n = n + nThisTime
			if err != nil {
				return
			}
			p = p[nThisTime:]
			off += int64(nThisTime)
		}
		index++
	}
	// At this point if there's anything left to read it means we've run off the
	// end of the file store. Read zeros. This is defined by the bittorrent protocol.
	for i, _ := range p {
		p[i] = 0
	}
	return
}

func (f *fileStore) WriteAt(p []byte, off int64) (int, error) {
	if f.cache != nil {
		needRawWrite := f.cache.WriteAt(p, off)
		if needRawWrite != nil {
			for _, nc := range needRawWrite {
				f.RawWriteAt(nc.Data, nc.I)
			}
		}
		return len(p), nil
	} else {
		return f.RawWriteAt(p, off)
	}
}

func (f *fileStore) Commit(pieceNum int, piece []byte, off int64) {
	if f.cache != nil {
		_, err := f.RawWriteAt(piece, off)
		if err != nil {
			log.Panicln("Error committing to storage:", err)
		}
		f.cache.MarkCommitted(pieceNum)
	}
}

func (f *fileStore) RawWriteAt(p []byte, off int64) (n int, err error) {
	index := f.find(off)
	for len(p) > 0 && index < len(f.offsets) {
		chunk := int64(len(p))
		entry := &f.files[index]
		itemOffset := off - f.offsets[index]
		if itemOffset < entry.length {
			space := entry.length - itemOffset
			if space < chunk {
				chunk = space
			}
			var nThisTime int
			nThisTime, err = entry.file.WriteAt(p[0:chunk], itemOffset)
			n += nThisTime
			if err != nil {
				return
			}
			p = p[nThisTime:]
			off += int64(nThisTime)
		}
		index++
	}
	// At this point if there's anything left to write it means we've run off the
	// end of the file store. Check that the data is zeros.
	// This is defined by the bittorrent protocol.
	for i, _ := range p {
		if p[i] != 0 {
			err = errors.New("Unexpected non-zero data at end of store.")
			n = n + i
			return
		}
	}
	n = n + len(p)
	return
}

func (f *fileStore) Close() (err error) {
	for i := range f.files {
		f.files[i].file.Close()
	}
	if f.cache != nil {
		f.cache.Close()
		f.cache = nil
	}
	if f.fileSystem != nil {
		err = f.fileSystem.Close()
	}
	return
}

func NewFileStore(info *InfoDict, fileSystem storage.FileSystem) (f FileStore, totalSize int64, err error) {
	numFiles := len(info.Files)
	if numFiles == 0 {
		// Create dummy Files structure.
		info = &InfoDict{Files: []FileDict{FileDict{info.Length, []string{info.Name}, info.Md5sum}}}
		numFiles = 1
	}
	fs := &fileStore{}
	fs.fileSystem = fileSystem
	fs.files = make([]fileEntry, numFiles)
	fs.offsets = make([]int64, numFiles)
	for i, _ := range info.Files {
		src := &info.Files[i]
		var file storage.File
		file, err = fs.fileSystem.Open(src.Path, src.Length)
		if err != nil {
			// Close all files opened up to now.
			for i2 := 0; i2 < i; i2++ {
				fs.files[i2].file.Close()
			}
			return
		}
		fs.files[i].file = file
		fs.files[i].length = src.Length
		fs.offsets[i] = totalSize
		totalSize += src.Length
	}
	f = fs
	return
}
