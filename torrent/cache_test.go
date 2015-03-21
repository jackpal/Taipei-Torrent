package torrent

import (
	"crypto/sha1"
	"encoding/hex"
	"io/ioutil"
	"log"
	"testing"
)

func TestCachedFileStoreRead(t *testing.T) {
	rcp := NewRamCacheProvider(2000)
	for _, testFile := range tests {
		fs, err := mkFileStore(testFile)
		orig, _ := ioutil.ReadFile(testFile.path)
		tC := rcp.NewCache("", 20000)
		tC.WriteAt(orig[128:512], 128)
		fs.SetCache(tC)
		if err != nil {
			t.Fatal(err)
		}
		ret := make([]byte, testFile.fileLen)
		_, err = fs.ReadAt(ret, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantedsum := sha1.Sum(orig[:testFile.fileLen])
		sum1Str := hex.EncodeToString(wantedsum[0:])
		gotsum := sha1.Sum(ret)
		sum2Str := hex.EncodeToString(gotsum[0:])
		if sum1Str != sum2Str {
			t.Errorf("Wanted %v, got %v\n", sum1Str, sum2Str)
			for i := 0; i < len(ret); i++ {
				if ret[i] != orig[i] {
					log.Println("Found a difference at", i, "wanted", orig[i], "got", ret[i])
					break
				}
			}
		}
	}
}
