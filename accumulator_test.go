package torrent

import (
	"testing"
	"time"
)

func TestAccumulator(t *testing.T) {
	start := time.Now()
	a := NewAccumulator(start, time.Minute)
	checkAcc(t, a, 0)
	a.Add(start, 1)
	checkAcc(t, a, 1)
	a.Add(start, 10)
	checkAcc(t, a, 11)
	checkRate(t, start, a, 11)
	middle := start.Add(time.Second)
	checkRate(t, middle, a, 5.5)
	a.Add(middle, 10)
	checkAcc(t, a, 21)
	checkRate(t, middle, a, 10.5)
}

func checkAcc(t *testing.T, a *Accumulator, expectedTotal int64) {
	total := a.getTotal()
	if total != expectedTotal {
		t.Errorf("Expected total %d actual %d", expectedTotal, total)
	}
}

func checkRate(t *testing.T, now time.Time, a *Accumulator, expectedRate float64) {
	rate := a.GetRate(now)
	if rate != expectedRate {
		t.Errorf("a.GetRate(%v) = %g. Expected %g", now, rate, expectedRate)
	}
}
