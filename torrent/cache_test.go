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
		numPieces := len(orig) / 512
		if len(orig)%512 > 0 {
			numPieces++
		}
		tC := rcp.NewCache("test", numPieces, 512, int64(len(orig)), fs)
		tC.WritePiece(orig[:512], 0)
		tC.WritePiece(orig[512:1024], 1)

		if err != nil {
			t.Fatal(err)
		}
		ret := make([]byte, testFile.fileLen)
		_, err = tC.ReadAt(ret, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantedsum := sha1.Sum(orig[:testFile.fileLen])
		sum1Str := hex.EncodeToString(wantedsum[0:])
		gotsum := sha1.Sum(ret)
		sum2Str := hex.EncodeToString(gotsum[0:])
		if sum1Str != sum2Str {
			t.Errorf("Wanted %v, got %v\n on cache read", sum1Str, sum2Str)
			for i := 0; i < len(ret); i++ {
				if ret[i] != orig[i] {
					log.Println("Found a difference at", i, "wanted", orig[i], "got", ret[i])
					break
				}
			}
		}

		ret = make([]byte, testFile.fileLen)
		_, err = fs.ReadAt(ret, 0)
		if err != nil {
			t.Fatal(err)
		}
		gotsum = sha1.Sum(ret)
		sum2Str = hex.EncodeToString(gotsum[0:])
		if sum1Str != sum2Str {
			t.Errorf("Wanted %v, got %v\n on filestore read", sum1Str, sum2Str)
			for i := 0; i < len(ret); i++ {
				if ret[i] != orig[i] {
					log.Println("Found a difference at", i, "wanted", orig[i], "got", ret[i])
					break
				}
			}
		}

		fs.Close()
	}
}
