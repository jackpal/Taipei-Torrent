package torrent

type ramFsProvider struct{}

func (o ramFsProvider) NewFS(directory string) (fs FileSystem, err error) {
	return &ramFileSystem{}, nil
}

// A RAM file system.
type ramFileSystem struct {
}

type ramFile []byte

func NewRAMFileSystem() (fs FileSystem, err error) {
	fs = &ramFileSystem{}
	return
}

func (r *ramFileSystem) Open(name []string, length int64) (file File, err error) {
	file = ramFile(make([]byte, int(length)))
	return
}

func (r *ramFileSystem) Close() error {
	return nil
}

func (r ramFile) ReadAt(p []byte, off int64) (n int, err error) {
	n = copy(p, []byte(r)[off:])
	return
}

func (r ramFile) WriteAt(p []byte, off int64) (n int, err error) {
	n = copy([]byte(r)[off:], p)
	return
}

func (r ramFile) Close() (err error) {
	return
}
