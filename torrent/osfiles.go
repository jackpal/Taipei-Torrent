package torrent

import (
	"errors"
	"os"
	"path"
	"strings"
)

// a torrent FileSystem that is backed by real OS files
type osFileSystem struct {
	storePath string
}

// A torrent File that is backed by an OS file
type osFile struct {
	filePath string
}

type OsFsProvider struct{}

func (o OsFsProvider) NewFS(directory string) (fs FileSystem, err error) {
	return &osFileSystem{directory}, nil
}

func (o *osFileSystem) Open(name []string, length int64) (file File, err error) {
	// Clean the source path before appending to the storePath. This
	// ensures that source paths that start with ".." can't escape.
	cleanSrcPath := path.Clean("/" + path.Join(name...))[1:]
	fullPath := path.Join(o.storePath, cleanSrcPath)
	err = ensureDirectory(fullPath)
	if err != nil {
		return
	}
	osfile := &osFile{fullPath}
	file = osfile
	err = osfile.ensureExists(length)
	return
}

func (o *osFileSystem) Close() error {
	return nil
}

func (o *osFile) Close() (err error) {
	return
}

func ensureDirectory(fullPath string) (err error) {
	fullPath = path.Clean(fullPath)
	if !strings.HasPrefix(fullPath, "/") {
		// Transform into absolute path.
		var cwd string
		if cwd, err = os.Getwd(); err != nil {
			return
		}
		fullPath = cwd + "/" + fullPath
	}
	base, _ := path.Split(fullPath)
	if base == "" {
		panic("Programming error: could not find base directory for absolute path " + fullPath)
	}
	err = os.MkdirAll(base, 0755)
	return
}

func (o *osFile) ensureExists(length int64) (err error) {
	name := o.filePath
	st, err := os.Stat(name)
	if err != nil && os.IsNotExist(err) {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
	} else {
		if st.Size() == length {
			return
		}
	}
	err = os.Truncate(name, length)
	if err != nil {
		err = errors.New("Could not truncate file.")
		return
	}
	return
}

func (o *osFile) ReadAt(p []byte, off int64) (n int, err error) {
	file, err := os.OpenFile(o.filePath, os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	return file.ReadAt(p, off)
}

func (o *osFile) WriteAt(p []byte, off int64) (n int, err error) {
	file, err := os.OpenFile(o.filePath, os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	return file.WriteAt(p, off)
}
