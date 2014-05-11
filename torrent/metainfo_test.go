package torrent

import (
	"testing"
)

func TestCreateMetaInfo(t *testing.T) {

}

func TestChoosePieceLength(t *testing.T) {
	for i := uint(0); i < 63; i++ {
		var totalLength int64 = 1 << i
		testOnePieceLength(t, totalLength)
		testOnePieceLength(t, totalLength*3/2)
	}
}

func testOnePieceLength(t *testing.T, totalLength int64) {
	pieceLength := choosePieceLength(totalLength)
	nextPowerOfTwoLength := int64(roundUpToPowerOfTwo(uint64(pieceLength)))
	if pieceLength < MinimumPieceLength {
		t.Errorf("choosePieceLen(%v) = %v. < %v", totalLength, pieceLength, MinimumPieceLength)
		return
	}
	if pieceLength != nextPowerOfTwoLength {
		t.Errorf("choosePieceLen(%v) = %v. Not Power of Two %v", totalLength, pieceLength, nextPowerOfTwoLength)
		return
	}
	if totalLength >= MinimumPieceLength*TargetPieceCountMin {
		pieces := (totalLength + pieceLength - 1) / pieceLength
		if pieces < TargetPieceCountMin || pieces >= TargetPieceCountMax {
			t.Errorf("choosePieceLen(%v) = %v. Pieces: %v. expected %v <= pieces < %v", totalLength, pieceLength,
				pieces, TargetPieceCountMin, TargetPieceCountMax)
			return
		}
	}
}
