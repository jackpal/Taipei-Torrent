package main

import (
	"io"
	"os"
	"strings"
	"syscall"
)

type FileStore interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
}

type fileEntry struct {
	length int64
	fd     *os.File
}

type fileStore struct {
	offsets []int64
	files   []fileEntry // Stored in increasing globalOffset order
}

func createPath(parts []string) (path string, err os.Error) {
	// TODO: better, OS-specific sanitization.
	for _, part := range parts {
		// Sanitize file names.
		if strings.Index(part, "/") >= 0 ||
			strings.Index(part, "\\") >= 0 ||
			part == ".." {
			err = os.NewError("Bad path part " + part)
			return
		}
	}

	path = strings.Join(parts, "/")
	return
}

func (fe *fileEntry) open(name string, length int64) (err os.Error) {
	fe.length = length
	fe.fd, err = os.Open(name, os.O_RDWR|os.O_CREAT, 0666)
	if err != nil {
		return
	}
	errno := syscall.Truncate(name, length)
	if errno != 0 {
		err = os.NewError("Could not truncate file.")
	}
	return
}

func ensureDirectory(fullPath string) (err os.Error) {
	pathParts := strings.Split(fullPath, "/", 0)
	if len(pathParts) < 2 {
		return
	}
	dirParts := pathParts[0 : len(pathParts)-1]
	path := strings.Join(dirParts, "/")
	err = os.MkdirAll(path, 0755)
	return
}

func NewFileStore(info *InfoDict, fileDir string) (f FileStore, totalSize int64, err os.Error) {
	fs := new(fileStore)
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
		var torrentPath string
		torrentPath, err = createPath(src.Path)
		if err != nil {
			return
		}
		fullPath := fileDir + "/" + torrentPath
		err = ensureDirectory(fullPath)
		if err != nil {
			return
		}
		err = fs.files[i].open(fullPath, src.Length)
		if err != nil {
			return
		}
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

func (f *fileStore) ReadAt(p []byte, off int64) (n int, err os.Error) {
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
			fd := entry.fd
			var nThisTime int
			nThisTime, err = fd.ReadAt(p[0:chunk], itemOffset)
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

func (f *fileStore) WriteAt(p []byte, off int64) (n int, err os.Error) {
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
			fd := entry.fd
			var nThisTime int
			nThisTime, err = fd.WriteAt(p[0:chunk], itemOffset)
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
			err = os.NewError("Unexpected non-zero data at end of store.")
			n = n + i
			return
		}
	}
	n = n + len(p)
	return
}

func (f *fileStore) Close() (err os.Error) {
	for i, _ := range f.files {
		fd := f.files[i].fd
		if fd != nil {
			fd.Close()
			f.files[i].fd = nil
		}
	}
	return
}
