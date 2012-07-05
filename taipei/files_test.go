package taipei

import (
	"crypto/sha1"
	"fmt"
	"os"
	"testing"
)

type testFile struct {
	path     string
	hashWant string
}

var tests []testFile = []testFile{{"testdata/file", "60CACBF3D72E1E7834203DA608037B1BF83B40E8"}}

func TestFileStoreRead(t *testing.T) {
	for _, testFile := range tests {

		fd, err := os.Open(testFile.path)
		if err != nil {
			t.Fatal(err)
		}
		fileLen := int64(1024)
		f := fileEntry{fileLen, fd}
		fs := fileStore{[]int64{0}, []fileEntry{f}}

		ret := make([]byte, fileLen)
		_, err = fs.ReadAt(ret, 0)
		if err != nil {
			t.Fatal(err)
		}
		h := sha1.New()
		h.Write(ret)
		sum := fmt.Sprintf("%X", h.Sum(nil))
		if sum != testFile.hashWant {
			t.Errorf("Wanted %v, got %v\n", testFile.hashWant, sum)
		}
	}
}
