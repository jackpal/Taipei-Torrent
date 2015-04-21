package storage

import (
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
