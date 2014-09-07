package torrent

import (
	"crypto/sha1"
	"fmt"
	"testing"
)

type testFile struct {
	path    string
	fileLen int64
	// SHA1 of fileLen bytes.
	hash string
	// SHA1 of the first 25 bytes only.
	hashPieceA string
	// SHA1 of bytes 25-49
	hashPieceB string
}

var tests []testFile = []testFile{{
	"testData/testFile",
	8054,
	// shasum testData/testFile | tr "[a-z]" "[A-Z]"
	"BC6314A1D1D36EC6C0888AF9DBD3B5E826612ADA",
	// dd if=testData/testFile bs=25 count=1 | shasum | tr "[a-z]" "[A-Z]"
	"F072A5A05C7ED8EECFFB6524FBFA89CA725A66C3",
	// dd if=testData/testFile bs=25 count=1 skip=1 | shasum | tr "[a-z]" "[A-Z]"
	"859CF11E055E61296F42EEB5BB19E598626A5173",
}}

func mkFileStore(tf testFile) (fs *fileStore, err error) {
	f := fileEntry{tf.fileLen, &osFile{tf.path}}
	return &fileStore{nil, []int64{0}, []fileEntry{f}}, nil
}

func TestFileStoreRead(t *testing.T) {
	for _, testFile := range tests {
		fs, err := mkFileStore(testFile)
		if err != nil {
			t.Fatal(err)
		}
		ret := make([]byte, testFile.fileLen)
		_, err = fs.ReadAt(ret, 0)
		if err != nil {
			t.Fatal(err)
		}
		h := sha1.New()
		h.Write(ret)
		sum := fmt.Sprintf("%X", h.Sum(nil))
		if sum != testFile.hash {
			t.Errorf("Wanted %v, got %v\n", testFile.hash, sum)
		}
	}
}
