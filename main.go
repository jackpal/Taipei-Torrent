package main

import (
	"flag"
	"log"
	"os"

	"github.com/uriel/Taipei-Torrent/taipei"
)

var torrent string
var debugp bool

func main() {
	flag.BoolVar(&debugp, "debug", false, "Turn on debugging")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()	
	if len(args) != 1 {
		log.Printf("Torrent file or torrent URL required.")
		usage()	
	}

	torrent = args[0]

	log.Println("Starting.")
	ts, err := taipei.NewTorrentSession(torrent)
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return
	}
	err = ts.DoTorrent()
	if err != nil {
		log.Println("Failed: ", err)
	} else {
		log.Println("Done")
	}
}

func usage() {
	log.Printf("usage: Taipei-Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}
