package main

import (
	"crypto/sha1"
	"fmt"
	"os"
	"testing"
)

type testFile struct {
	path    string
	fileLen int64
	hash    string
	// SHA1 of the first 25 bytes only.
	hashPieceA string
	// SHA1 of bytes 25-49
	hashPieceB string
}

var tests []testFile = []testFile{{
	"testData/file",
	1024,
	"A0AD08765665C1339E2F829F4EBFF598B355A62B",
	// dd if=testdata/file bs=25 count=1 | shasum
	"4582F29D1C80210E6B1D4BACF772572E6F4518FB",
	// dd if=testdata/file bs=25 count=1 skip=1 | shasum
	"2676C88B22E905E9DC0BD437533ED69E5D899EF4",
}}

func mkFileStore(tf testFile) (fs *fileStore, err error) {
	fd, err := os.Open(tf.path)
	if err != nil {
		return fs, err
	}
	f := fileEntry{tf.fileLen, fd}
	return &fileStore{[]int64{0}, []fileEntry{f}}, nil
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
