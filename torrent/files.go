package torrent

import (
	"errors"
	"io"
)

// Interface for a file.
// Multiple goroutines may access a File at the same time.
type File interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
}

//Interface for a provider of filesystems.
type FsProvider interface {
	NewFS(directory string) (FileSystem, error)
}

// Interface for a file system. A file system contains files.
type FileSystem interface {
	Open(name []string, length int64) (file File, err error)
	io.Closer
}

// A torrent file store.
// WritePiece should be called for full, verified pieces only;
type FileStore interface {
	io.ReaderAt
	io.Closer
	WritePiece(buffer []byte, piece int) (written int, err error)
}

type fileStore struct {
	fileSystem FileSystem
	offsets    []int64
	files      []fileEntry // Stored in increasing globalOffset order
	pieceSize  int64
}

type fileEntry struct {
	length int64
	file   File
}

func NewFileStore(info *InfoDict, fileSystem FileSystem) (f FileStore, totalSize int64, err error) {
	fs := &fileStore{}
	fs.fileSystem = fileSystem
	fs.pieceSize = info.PieceLength
	numFiles := len(info.Files)
	if numFiles == 0 {
		// Create dummy Files structure.
		info = &InfoDict{Files: []FileDict{FileDict{info.Length, []string{info.Name}, info.Md5sum}}}
		numFiles = 1
	}
	fs.files = make([]fileEntry, numFiles)
	fs.offsets = make([]int64, numFiles)
	for i, _ := range info.Files {
		src := &info.Files[i]
		var file File
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

func (f *fileStore) ReadAt(p []byte, off int64) (n int, err error) {
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

func (f *fileStore) WritePiece(p []byte, piece int) (n int, err error) {
	off := int64(piece) * f.pieceSize
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

	if f.fileSystem != nil {
		err = f.fileSystem.Close()
	}
	return
}
