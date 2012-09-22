package main

import (
	"flag"
	"log"
	"os"
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	narg := flag.NArg()
	if narg != 1 {
		if narg < 1 {
			log.Println("Too few arguments. Torrent file or torrent URL required.")
		} else {
			log.Printf("Too many arguments. (Expected 1): %v", args)
		}
		usage()
	}

	torrent = args[0]

	log.Println("Starting.")
	ts, err := NewTorrentSession(torrent)
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
