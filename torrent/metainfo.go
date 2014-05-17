package torrent

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"

	bencode "code.google.com/p/bencode-go"
	"github.com/nictuku/dht"
)

type FileDict struct {
	Length int64
	Path   []string
	Md5sum string
}

type InfoDict struct {
	PieceLength int64 "piece length"
	Pieces      string
	Private     int64
	Name        string
	// Single File Mode
	Length int64
	Md5sum string
	// Multiple File mode
	Files []FileDict
}

type MetaInfo struct {
	Info         InfoDict
	InfoHash     string
	Announce     string
	AnnounceList [][]string "announce-list"
	CreationDate string     "creation date"
	Comment      string
	CreatedBy    string "created by"
	Encoding     string
}

func getString(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Parse a list of list of strings structure, filtering out anything that's
// not a string, and filtering out empty lists. May return nil.
func getSliceSliceString(m map[string]interface{}, k string) (aas [][]string) {
	if a, ok := m[k]; ok {
		if b, ok := a.([]interface{}); ok {
			for _, c := range b {
				if d, ok := c.([]interface{}); ok {
					var sliceOfStrings []string
					for _, e := range d {
						if f, ok := e.(string); ok {
							sliceOfStrings = append(sliceOfStrings, f)
						}
					}
					if len(sliceOfStrings) > 0 {
						aas = append(aas, sliceOfStrings)
					}
				}
			}
		}
	}
	return
}

func GetMetaInfo(torrent string) (metaInfo *MetaInfo, err error) {
	var input io.ReadCloser
	if strings.HasPrefix(torrent, "http:") {
		r, err := proxyHttpGet(torrent)
		if err != nil {
			return nil, err
		}
		input = r.Body
	} else if strings.HasPrefix(torrent, "magnet:") {
		magnet, err := parseMagnet(torrent)
		if err != nil {
			log.Println("Couldn't parse magnet: ", err)
			return nil, err
		}

		ih, err := dht.DecodeInfoHash(magnet.InfoHashes[0])
		if err != nil {
			return nil, err
		}

		metaInfo = &MetaInfo{InfoHash: string(ih)}
		return metaInfo, err

	} else {
		if input, err = os.Open(torrent); err != nil {
			return
		}
	}

	// We need to calcuate the sha1 of the Info map, including every value in the
	// map. The easiest way to do this is to read the data using the Decode
	// API, and then pick through it manually.
	var m interface{}
	m, err = bencode.Decode(input)
	input.Close()
	if err != nil {
		err = errors.New("Couldn't parse torrent file phase 1: " + err.Error())
		return
	}

	topMap, ok := m.(map[string]interface{})
	if !ok {
		err = errors.New("Couldn't parse torrent file phase 2.")
		return
	}

	infoMap, ok := topMap["info"]
	if !ok {
		err = errors.New("Couldn't parse torrent file. info")
		return
	}
	var b bytes.Buffer
	if err = bencode.Marshal(&b, infoMap); err != nil {
		return
	}
	hash := sha1.New()
	hash.Write(b.Bytes())

	var m2 MetaInfo
	err = bencode.Unmarshal(&b, &m2.Info)
	if err != nil {
		return
	}

	m2.InfoHash = string(hash.Sum(nil))
	m2.Announce = getString(topMap, "announce")
	m2.AnnounceList = getSliceSliceString(topMap, "announce-list")
	m2.CreationDate = getString(topMap, "creation date")
	m2.Comment = getString(topMap, "comment")
	m2.CreatedBy = getString(topMap, "created by")
	m2.Encoding = getString(topMap, "encoding")

	metaInfo = &m2
	return
}

type MetaInfoFileSystem interface {
	Open(name string) (MetaInfoFile, error)
	Stat(name string) (os.FileInfo, error)
}

type MetaInfoFile interface {
	io.Closer
	io.Reader
	io.ReaderAt
	Readdirnames(n int) (names []string, err error)
	Stat() (os.FileInfo, error)
}

type OSMetaInfoFileSystem struct {
	dir string
}

func (o *OSMetaInfoFileSystem) Open(name string) (MetaInfoFile, error) {
	return os.Open(path.Join(o.dir, name))
}

func (o *OSMetaInfoFileSystem) Stat(name string) (os.FileInfo, error) {
	return os.Stat(path.Join(o.dir, name))
}

// Adapt a MetaInfoFileSystem into a torrent file store FileSystem
type FileStoreFileSystemAdapter struct {
	m MetaInfoFileSystem
}

type FileStoreFileAdapter struct {
	f MetaInfoFile
}

func (f *FileStoreFileSystemAdapter) Open(name []string, length int64) (file File, err error) {
	var ff MetaInfoFile
	ff, err = f.m.Open(path.Join(name...))
	if err != nil {
		return
	}
	stat, err := ff.Stat()
	if err != nil {
		return
	}
	actualSize := stat.Size()
	if actualSize != length {
		err = fmt.Errorf("Unexpected file size %v. Expected %v", actualSize, length)
		return
	}
	file = &FileStoreFileAdapter{ff}
	return
}

func (f *FileStoreFileAdapter) ReadAt(p []byte, off int64) (n int, err error) {
	return f.f.ReadAt(p, off)
}

func (f *FileStoreFileAdapter) WriteAt(p []byte, off int64) (n int, err error) {
	// Writes must match existing data exactly.
	q := make([]byte, len(p))
	_, err = f.ReadAt(q, off)
	if err != nil {
		return
	}
	if bytes.Compare(p, q) != 0 {
		err = fmt.Errorf("New data does not match original data.")
	}
	return
}

func (f *FileStoreFileAdapter) Close() (err error) {
	return f.f.Close()
}

// Create a MetaInfo for a given file and file system.
// If fs is nil then the OSMetaInfoFileSystem will be used.
// If pieceLength is 0 then an optimal piece length will be chosen.
func CreateMetaInfoFromFileSystem(fs MetaInfoFileSystem, root string, pieceLength int64, wantMD5Sum bool) (metaInfo *MetaInfo, err error) {
	if fs == nil {
		dir, file := path.Split(root)
		fs = &OSMetaInfoFileSystem{dir}
		root = file
	}
	var m *MetaInfo = &MetaInfo{}
	var fileInfo os.FileInfo
	fileInfo, err = fs.Stat(root)
	var totalLength int64
	if fileInfo.IsDir() {
		err = m.addFiles(fs, root)
		if err != nil {
			return
		}
		for i := range m.Info.Files {
			totalLength += m.Info.Files[i].Length
		}
		if wantMD5Sum {
			for i := range m.Info.Files {
				fd := &m.Info.Files[i]
				fd.Md5sum, err = md5Sum(fs, path.Join(fd.Path...))
				if err != nil {
					return
				}
			}
		}
	} else {
		m.Info.Name = path.Base(root)
		totalLength = fileInfo.Size()
		m.Info.Length = totalLength
		if wantMD5Sum {
			m.Info.Md5sum, err = md5Sum(fs, root)
			if err != nil {
				return
			}
		}
	}
	if pieceLength == 0 {
		pieceLength = choosePieceLength(totalLength)
	}
	m.Info.PieceLength = int64(pieceLength)
	fileStoreFS := &FileStoreFileSystemAdapter{fs}
	var fileStore FileStore
	var fileStoreLength int64
	fileStore, fileStoreLength, err = NewFileStore(&m.Info, fileStoreFS)
	if err != nil {
		return
	}
	if fileStoreLength != totalLength {
		err = fmt.Errorf("Filestore total length %v, expected %v", fileStoreLength, totalLength)
		return
	}
	var sums []byte
	sums, err = computeSums(fileStore, totalLength, int64(pieceLength))
	if err != nil {
		return
	}
	m.Info.Pieces = string(sums)
	m.UpdateInfoHash(metaInfo)
	metaInfo = m
	return
}

const MinimumPieceLength = 16 * 1024
const TargetPieceCountLog2 = 10
const TargetPieceCountMin = 1 << TargetPieceCountLog2

// Target piece count should be < TargetPieceCountMax
const TargetPieceCountMax = TargetPieceCountMin << 1

// Choose a good piecelength.
func choosePieceLength(totalLength int64) (pieceLength int64) {
	// Must be a power of 2.
	// Must be a multiple of 16KB
	// Prefer to provide around 1024..2048 pieces.
	pieceLength = MinimumPieceLength
	pieces := totalLength / pieceLength
	for pieces >= TargetPieceCountMax {
		pieceLength <<= 1
		pieces >>= 1
	}
	return
}

func roundUpToPowerOfTwo(v uint64) uint64 {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v |= v >> 32
	v++
	return v
}

func WriteMetaInfoBytes(root string, w io.Writer) (err error) {
	var m *MetaInfo
	m, err = CreateMetaInfoFromFileSystem(nil, root, 0, true)
	if err != nil {
		return
	}
	// log.Printf("Metainfo: %#v", m)
	err = m.Bencode(w)
	if err != nil {
		return
	}
	return
}

func md5Sum(fs MetaInfoFileSystem, file string) (sum string, err error) {
	var f MetaInfoFile
	f, err = fs.Open(file)
	if err != nil {
		return
	}
	defer f.Close()
	hash := md5.New()
	_, err = io.Copy(hash, f)
	if err != nil {
		return
	}
	sum = string(hash.Sum(nil))
	return
}

func (m *MetaInfo) addFiles(fs MetaInfoFileSystem, file string) (err error) {
	var fileInfo os.FileInfo
	fileInfo, err = fs.Stat(file)
	if err != nil {
		return
	}
	if fileInfo.IsDir() {
		var f MetaInfoFile
		f, err = fs.Open(file)
		if err != nil {
			return
		}
		var fi []string
		fi, err = f.Readdirnames(0)
		if err != nil {
			return
		}
		for _, name := range fi {
			err = m.addFiles(fs, path.Join(file, name))
			if err != nil {
				return
			}
		}
	} else {
		fileDict := FileDict{Length: fileInfo.Size()}
		cleanFile := path.Clean(file)
		parts := strings.Split(cleanFile, string(os.PathSeparator))
		fileDict.Path = parts
		m.Info.Files = append(m.Info.Files, fileDict)
	}
	return
}

// Updates the InfoHash field. Call this after manually changing the Info data.
func (m *MetaInfo) UpdateInfoHash(metaInfo *MetaInfo) (err error) {
	var b bytes.Buffer
	infoMap := m.Info.toMap()
	if len(infoMap) > 0 {
		err = bencode.Marshal(&b, infoMap)
		if err != nil {
			return
		}
	}
	hash := sha1.New()
	hash.Write(b.Bytes())

	m.InfoHash = string(hash.Sum(nil))
	return
}

// Copy the non-default values from an InfoDict to a map.
func (i *InfoDict) toMap() (m map[string]interface{}) {
	id := map[string]interface{}{}
	// InfoDict
	if i.PieceLength != 0 {
		id["piece length"] = i.PieceLength
	}
	if i.Pieces != "" {
		id["pieces"] = i.Pieces
	}
	if i.Private != 0 {
		id["private"] = i.Private
	}
	if i.Name != "" {
		id["name"] = i.Name
	}
	if i.Length != 0 {
		id["length"] = i.Length
	}
	if i.Md5sum != "" {
		id["md5sum"] = i.Md5sum
	}
	if len(i.Files) > 0 {
		var fi []map[string]interface{}
		for ii := range i.Files {
			f := &i.Files[ii]
			fd := map[string]interface{}{}
			if f.Length > 0 {
				fd["length"] = f.Length
			}
			if len(f.Path) > 0 {
				fd["path"] = f.Path
			}
			if f.Md5sum != "" {
				fd["md5sum"] = f.Md5sum
			}
			if len(fd) > 0 {
				fi = append(fi, fd)
			}
		}
		if len(fi) > 0 {
			id["files"] = fi
		}
	}
	if len(id) > 0 {
		m = id
	}
	return
}

// Encode to Bencode, but only encode non-default values.
func (m *MetaInfo) Bencode(w io.Writer) (err error) {
	var mi map[string]interface{} = map[string]interface{}{}
	id := m.Info.toMap()
	if len(id) > 0 {
		mi["info"] = id
	}
	// Do not encode InfoHash. Clients are supposed to calculate it themselves.
	if m.Announce != "" {
		mi["announce"] = m.Announce
	}
	if len(m.AnnounceList) > 0 {
		mi["announce-list"] = m.AnnounceList
	}
	if m.CreationDate != "" {
		mi["creation date"] = m.CreationDate
	}
	if m.Comment != "" {
		mi["comment"] = m.Comment
	}
	if m.CreatedBy != "" {
		mi["created by"] = m.CreatedBy
	}
	if m.Encoding != "" {
		mi["encoding"] = m.Encoding
	}
	bencode.Marshal(w, mi)
	return
}

type TrackerResponse struct {
	FailureReason  string "failure reason"
	WarningMessage string "warning message"
	Interval       uint
	MinInterval    uint   "min interval"
	TrackerId      string "tracker id"
	Complete       uint
	Incomplete     uint
	Peers          string
	Peers6         string
}

type SessionInfo struct {
	PeerId     string
	Port       uint16
	Uploaded   uint64
	Downloaded uint64
	Left       uint64

	UseDHT      bool
	FromMagnet  bool
	HaveTorrent bool

	OurExtensions map[int]string
	ME            *MetaDataExchange
}

type MetaDataExchange struct {
	Transferring bool
	Pieces       [][]byte
}

func getTrackerInfo(url string) (tr *TrackerResponse, err error) {
	r, err := proxyHttpGet(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		data, _ := ioutil.ReadAll(r.Body)
		reason := "Bad Request " + string(data)
		log.Println(reason)
		err = errors.New(reason)
		return
	}
	var tr2 TrackerResponse
	err = bencode.Unmarshal(r.Body, &tr2)
	r.Body.Close()
	if err != nil {
		return
	}
	tr = &tr2
	return
}

func saveMetaInfo(metadata string) (err error) {
	var info InfoDict
	err = bencode.Unmarshal(bytes.NewReader([]byte(metadata)), &info)
	if err != nil {
		return
	}

	f, err := os.Create(info.Name + ".torrent")
	if err != nil {
		log.Println("Error when opening file for creation: ", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(metadata)

	return
}
