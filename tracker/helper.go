package tracker

import "log"

func NewActiveTracker() *Tracker {
	torrentTracker := NewTracker()
	torrentTracker.Addr = ":8085"

	go func() {
		log.Println("Serve torrents, listening at ", torrentTracker.Addr)
		err := torrentTracker.ListenAndServe()
		if err != nil {
			panic(err)
		}
	}()
	return torrentTracker
}
