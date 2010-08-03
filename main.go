package main

import (
	"flag"
	"log"
)

func main() {
	// testBencode()
	// testUPnP()
	flag.Parse()
	log.Stderr("Starting.")
	listenPort, err := chooseListenPort()
	if err != nil {
		log.Stderr("Could not choose listen port. Peer connectivity will be affected.")
	}
	ts, err := NewTorrentSession(torrent, listenPort)
	if err != nil {
		log.Stderr("Could not create torrent session.", err)
		return
	}
	err = ts.DoTorrent(listenPort)
	if err != nil {
		log.Stderr("Failed: ", err)
	} else {
		log.Stderr("Done")
	}
}
