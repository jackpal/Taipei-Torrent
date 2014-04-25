package torrent

import (
	"crypto/sha1"
	"fmt"
	"testing"
)

func TestComputeSums(t *testing.T) {
	pieceLen := int64(25)
	for _, testFile := range tests {
		fs, err := mkFileStore(testFile)
		if err != nil {
			t.Fatal(err)
		}
		sums, err := computeSums(fs, testFile.fileLen, pieceLen)
		if err != nil {
			t.Fatal(err)
		}
		if len(sums) < sha1.Size {
			t.Errorf("computeSums got len %d, wanted %d", len(sums), sha1.Size)
		}
		a := fmt.Sprintf("%X", sums[:sha1.Size])
		b := fmt.Sprintf("%X", sums[sha1.Size:sha1.Size*2])
		if a != testFile.hashPieceA {
			t.Errorf("Piece A Wanted %v, got %v\n", testFile.hashPieceA, a)
		}
		if b != testFile.hashPieceB {
			t.Errorf("Piece B Wanted %v, got %v\n", testFile.hashPieceB, b)
		}
	}
}
