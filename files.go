package main

import (
    "io"
    "os"
    "syscall"
)

type FileStore interface {
    io.ReaderAt
    io.WriterAt
    io.Closer
}

type fileEntry struct {
    length int64
    fd *os.File
}

type fileStore struct {
    offsets []int64
    files []fileEntry // Stored in increasing globalOffset order
}

func createPath(parts []string) (path string) {
    for i, part := range(parts) {
        if i > 0 {
            path = path + "/"
        }
        path = path + part
    }
    return
}

func (fe *fileEntry) open(name string, length int64) (err os.Error) {
    // TODO: sanitize name
    fe.fd, err = os.Open(name, os.O_RDWR | os.O_CREAT, 0666)
    if err != nil {
        return
    }
    errno := syscall.Truncate(name, length)
    if errno != 0 {
        err = os.NewError("Could not truncate file.")
    }
    return
}

func NewFileStore(info *InfoDict) (f FileStore, totalSize int64, err os.Error) {
    fs := new(fileStore)
    numFiles := len(info.Files)
    if numFiles == 0 {
        fs.files = make([]fileEntry, 1)
        fs.offsets = make([]int64, 1)
        err = fs.files[0].open(info.Name, info.Length)
        if err != nil {
            return
        }
        totalSize = info.Length
    } else {
        fs.files = make([]fileEntry, numFiles)
        fs.offsets = make([]int64, numFiles)
        for i,_ := range(info.Files) {
            src := &info.Files[i]
            err = fs.files[i].open(createPath(src.Path), src.Length)
			if err != nil {
				return
			}
            fs.offsets[i] = totalSize
            totalSize += src.Length
	    }
	}
	f = fs
	return
}

func (f* fileStore) find(offset int64) int {
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
        entry := & f.files[index]
        itemOffset := off - f.offsets[index]
        if itemOffset < entry.length {
            space := entry.length - itemOffset
            if space < chunk {
                chunk = space
            }
            fd := entry.fd
            nThisTime, err := fd.ReadAt(p[0:chunk], itemOffset)
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
    for i,_ := range(p) {
        p[i] = 0
    }
    return
}

func (f *fileStore) WriteAt(p []byte, off int64) (n int, err os.Error) {
    index := f.find(off)
    for len(p) > 0 && index < len(f.offsets) {
        chunk := int64(len(p))
        entry := & f.files[index]
        itemOffset := off - f.offsets[index]
        if itemOffset < entry.length {
            space := entry.length - itemOffset
            if space < chunk {
                chunk = space
            }
            fd := entry.fd
            nThisTime, err := fd.WriteAt(p[0:chunk], itemOffset)
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
    for i,_ := range(p) {
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
    for i, _ := range(f.files) {
        fd := f.files[i].fd
        if fd != nil {
            fd.Close()
            f.files[i].fd = nil
        }
    }
    return
}

