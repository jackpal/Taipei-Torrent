package torrent

import (
	"fmt"
	"math"
	"testing"
)

type testChoker struct {
	name        string
	downloadBPS float32
}

func (t *testChoker) DownloadBPS() float32 {
	return t.downloadBPS
}

func (t *testChoker) String() string {
	return fmt.Sprintf("{%#v, %g}", t.name, t.downloadBPS)
}

var chokersSets [][]*testChoker = [][]*testChoker{
	[]*testChoker{},
	[]*testChoker{{"a", 0}},
	[]*testChoker{{"a", 0}, {"b", 1}},
	[]*testChoker{{"a", 0}, {"b", 1}, {"c", 2}},
	[]*testChoker{{"a", 0}, {"b", 1}, {"c", 2}, {"d", 3}},
	[]*testChoker{{"a", 0}, {"b", 1}, {"c", 2}, {"d", 3},
		{"e", 4}, {"f", 5}, {"g", 6}},
}

func toChokerSlice(chokers []*testChoker) (result []Choker) {
	result = make([]Choker, len(chokers))
	for i, c := range chokers {
		result[i] = Choker(c)
	}
	return
}

func TestNeverChokePolicy(t *testing.T) {
	for _, chokers := range chokersSets {
		policy := NeverChokePolicy{}
		candidates := toChokerSlice(chokers)
		candidatesCopy := append([]Choker{}, candidates...)
		unchokeCount, err := policy.Choke(candidates)
		if err != nil || unchokeCount != len(candidates) ||
			!similar(candidates, candidatesCopy) {
			t.Errorf("NeverChokePolicy.Choke(%v) => %v, %d, %v",
				candidatesCopy, candidates, unchokeCount, err)
		}
	}
}

// Check that a and b both have the same elements (different order).
func similar(a, b []Choker) bool {
	if len(a) != len(b) {
		return false
	}
	// O(n^2)
	for _, aa := range a {
		found := false
		for _, bb := range b {
			if aa == bb {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestClassicChokePolicy(t *testing.T) {
	for _, chokers := range chokersSets {
		policy := ClassicChokePolicy{}
		candidates := toChokerSlice(chokers)
		candidatesCopy := append([]Choker{}, candidates...)
		unchokeCount, err := policy.Choke(candidates)
		expectedUnchokeCount := len(candidates)
		maxUnchokeCount := OPTIMISTIC_UNCHOKE_INDEX + 1
		if expectedUnchokeCount > maxUnchokeCount {
			expectedUnchokeCount = maxUnchokeCount
		}
		if err != nil || unchokeCount != expectedUnchokeCount ||
			!similar(candidates, candidatesCopy) ||
			!verifyClassicSortOrder(candidates, HIGH_BANDWIDTH_SLOTS) {
			t.Errorf("ClassicChokePolicy.Choke(%v) => %v, %d, %v",
				candidatesCopy, candidates, unchokeCount, err)
		}
	}
}

func verifyClassicSortOrder(a []Choker, highBandwidthSlotCount int) bool {
	var lowestHighBandwidthSlotBps float32 = float32(math.Inf(0))
	for i, aa := range a {
		bps := aa.DownloadBPS()
		if i < highBandwidthSlotCount {
			if bps < lowestHighBandwidthSlotBps {
				lowestHighBandwidthSlotBps = bps
			}
		} else if bps > lowestHighBandwidthSlotBps {
			return false
		}
	}
	return true
}
