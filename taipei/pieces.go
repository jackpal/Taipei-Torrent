// Compute missing pieces for a torrent.
package taipei

import (
	"crypto/sha1"
	"errors"
	"runtime"
)

func checkPieces(fs FileStore, totalLength int64, m *MetaInfo) (good, bad int64, goodBits *Bitset, err error) {
	pieceLength := m.Info.PieceLength
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	goodBits = NewBitset(int(numPieces))
	ref := m.Info.Pieces
	if len(ref) != int(numPieces*sha1.Size) {
		err = errors.New("Incorrect Info.Pieces length")
		return
	}
	currentSums, err := computeSums(fs, totalLength, m.Info.PieceLength)
	if err != nil {
		return
	}
	for i := int64(0); i < numPieces; i++ {
		base := i * sha1.Size
		end := base + sha1.Size
		if checkEqual(ref[base:end], currentSums[base:end]) {
			good++
			goodBits.Set(int(i))
		} else {
			bad++
		}
	}
	return
}

func checkEqual(ref string, current []byte) bool {
	for i := 0; i < len(current); i++ {
		if ref[i] != current[i] {
			return false
		}
	}
	return true
}

// computeSums reads the file content and computes the SHA1 hash for each
// piece. Spawns parallel goroutines to compute the hashes, since each
// computation takes ~30ms.
func computeSums(fs FileStore, totalLength int64, pieceLength int64) (sums []byte, err error) {
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	sums = make([]byte, sha1.Size*numPieces)
	pieces := make(chan []byte, 10)

	// Read file content and sends to "pieces", keeping order.
	go func() {
		piece := make([]byte, pieceLength)
		for i := int64(0); i < numPieces; i++ {
			if i == numPieces-1 {
				piece = piece[0 : totalLength-i*pieceLength]
			}
			_, err = fs.ReadAt(piece, i*pieceLength)
			if err != nil {
				pieces <- nil
			} else {
				pieces <- piece
			}
		}
		close(pieces)
	}()

	// Calculate the SHA1 hash for each piece, in parallel goroutines.
	numHashRoutines := int64(runtime.GOMAXPROCS(0))
	hashers := make([]chan piece, numHashRoutines, 5)
	results := make([]chan pieceHash, numHashRoutines)
	for shard := int64(0); shard < numHashRoutines; shard++ {
		hashers[shard] = make(chan piece)
		results[shard] = make(chan pieceHash)
		go hashPiece(hashers[shard], results[shard])
	}
	go func() {
		i := int64(0)
		for d := range pieces {
			hashers[i%numHashRoutines] <- piece{i, d}
			i++
		}
	}()

	// Merge back the results.
	for i := int64(0); i < numPieces; i++ {
		for shard := int64(0); shard < numHashRoutines; shard++ {
			h := <-results[shard]
			copy(sums[h.i*sha1.Size:], h.hash)
			i++
		}
	}
	return
}

type piece struct {
	i    int64
	data []byte
}

type pieceHash struct {
	i    int64
	hash []byte
}

func hashPiece(pieces chan piece, result chan pieceHash) {
	hasher := sha1.New()
	i := int64(0)
	for piece := range pieces {
		hasher.Reset()
		_, err := hasher.Write(piece.data)
		if err != nil {
			result <- pieceHash{i, nil}
		} else {
			result <- pieceHash{i, hasher.Sum(nil)}
		}
		i++
	}
}

func checkPiece(fs FileStore, totalLength int64, m *MetaInfo, pieceIndex int) (good bool, err error) {
	ref := m.Info.Pieces
	currentSum, err := computePieceSum(fs, totalLength, m.Info.PieceLength, pieceIndex)
	if err != nil {
		return
	}
	base := pieceIndex * sha1.Size
	end := base + sha1.Size
	good = checkEqual(ref[base:end], currentSum)
	return
}

func computePieceSum(fs FileStore, totalLength int64, pieceLength int64, pieceIndex int) (sum []byte, err error) {
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	hasher := sha1.New()
	piece := make([]byte, pieceLength)
	if int64(pieceIndex) == numPieces-1 {
		piece = piece[0 : totalLength-int64(pieceIndex)*pieceLength]
	}
	_, err = fs.ReadAt(piece, int64(pieceIndex)*pieceLength)
	if err != nil {
		return
	}
	_, err = hasher.Write(piece)
	if err != nil {
		return
	}
	sum = hasher.Sum(nil)
	return
}
