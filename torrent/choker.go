package torrent

import (
	"math/rand"
	"sort"
)

// BitTorrent choking policy.

// The choking policy's view of a peer. For current policies we only care
// about identity and download bandwidth.
type Choker interface {
	DownloadBPS() float32 // bps
}

type ChokePolicy interface {
	// Only pass in interested peers.
	// mutate the chokers into a list where the first N are to be unchoked.
	Choke(chokers []Choker) (unchokeCount int, err error)
}

// Our naive never-choke policy
type NeverChokePolicy struct{}

func (n *NeverChokePolicy) Choke(chokers []Choker) (unchokeCount int, err error) {
	return len(chokers), nil
}

// Our interpretation of the classic bittorrent choke policy.
// Expects to be called once every 10 seconds.
// See the section "Choking and optimistic unchoking" in
// https://wiki.theory.org/BitTorrentSpecification
type ClassicChokePolicy struct {
	optimisticUnchoker Choker // The choker we unchoked optimistically
	counter            int    // When to choose a new optimisticUnchoker.
}

type ByDownloadBPS []Choker

func (a ByDownloadBPS) Len() int {
	return len(a)
}

func (a ByDownloadBPS) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByDownloadBPS) Less(i, j int) bool {
	return a[i].DownloadBPS() > a[j].DownloadBPS()
}

const HIGH_BANDWIDTH_SLOTS = 3
const OPTIMISTIC_UNCHOKE_INDEX = HIGH_BANDWIDTH_SLOTS

// How many cycles of this algorithm before we pick a new optimistic
const OPTIMISTIC_UNCHOKE_COUNT = 3

func (ccp *ClassicChokePolicy) Choke(chokers []Choker) (unchokeCount int, err error) {
	sort.Sort(ByDownloadBPS(chokers))

	optimistIndex := ccp.findOptimist(chokers)
	if optimistIndex >= 0 {
		if optimistIndex < OPTIMISTIC_UNCHOKE_INDEX {
			// Forget optimistic choke
			optimistIndex = -1
		} else {
			ByDownloadBPS(chokers).Swap(OPTIMISTIC_UNCHOKE_INDEX, optimistIndex)
			optimistIndex = OPTIMISTIC_UNCHOKE_INDEX
		}
	}

	if optimistIndex >= 0 {
		ccp.counter++
		if ccp.counter >= OPTIMISTIC_UNCHOKE_COUNT {
			ccp.counter = 0
			optimistIndex = -1
		}
	}

	if optimistIndex < 0 {
		candidateCount := len(chokers) - OPTIMISTIC_UNCHOKE_INDEX
		if candidateCount > 0 {
			candidate := OPTIMISTIC_UNCHOKE_INDEX + rand.Intn(candidateCount)
			ByDownloadBPS(chokers).Swap(OPTIMISTIC_UNCHOKE_INDEX, candidate)
			ccp.counter = 0
			ccp.optimisticUnchoker = chokers[OPTIMISTIC_UNCHOKE_INDEX]
		}
	}
	unchokeCount = OPTIMISTIC_UNCHOKE_INDEX + 1
	if unchokeCount > len(chokers) {
		unchokeCount = len(chokers)
	}
	return
}

func (ccp *ClassicChokePolicy) findOptimist(chokers []Choker) (index int) {
	for i, c := range chokers {
		if c == ccp.optimisticUnchoker {
			return i
		}
	}
	return -1
}
