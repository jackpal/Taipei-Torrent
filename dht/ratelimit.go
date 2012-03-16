package dht

import (
	"time"

	"code.google.com/p/vitess/go/cache"
)

const (
	maxPerMinute = 10
	maxHosts     = 1000
)

func NewThrottler() *clientThrottle {
	r := clientThrottle{
		c:       cache.NewLRUCache(maxHosts),
		blocked: cache.NewLRUCache(maxHosts),
	}
	go r.cleanup()
	return &r
}

type clientThrottle struct {
	// Rate limiter.
	c *cache.LRUCache

	// Hosts that get blocked once go to a separate cache, and stay forever
	// until they stop hitting us enough to fall off the blocked cache.
	blocked *cache.LRUCache
}

func (r *clientThrottle) checkBlock(host string) bool {
	_, blocked := r.blocked.Get(host)
	if blocked {
		// Bad guy stays there.
		return false
	}

	v, ok := r.c.Get(host)
	var h hits
	if !ok {
		h = hits(59)
	} else {
		h = v.(hits) - 1
	}
	if h < 60-maxPerMinute {
		r.c.Set(host, h-300)
		// New bad guy.
		r.blocked.Set(host, h) // The value here is not relevant.
		return false
	}
	r.c.Set(host, h)
	return true
}

// refill the buckets.
// this is the first way I thought of how to implement client rate limiting.
// Need to think and research more.
func (r *clientThrottle) cleanup() {
	// Check the bucket faster than the rate period, to reduce the pressure in the cache.
	t := time.Tick(5 * time.Second)

	// This is ridiculously inefficient but it'll have to do for now.
	for _ = range t {
		var h hits
		for _, item := range r.c.Items() {
			h = item.Value.(hits) + 5
			if h > 60 {
				// Reduce pressure in the LRU.
				r.c.Delete(item.Key)
			} else {
				r.c.Set(item.Key, h)
			}
		}
	}
}

type hits int

func (h hits) Size() int {
	return 1
}
