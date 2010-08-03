package main

import (
	"flag"
	"log"
	"os"
	"taipei"
)

var torrent string
var debugp bool

func init() {
	flag.StringVar(&torrent, "torrent", "", "URL or path to a torrent file (Required)")
	flag.BoolVar(&debugp, "debug", false, "Turn on debugging")

	// Check required flags.
	req := []interface{}{"torrent"}
	for _, n := range req {
		f := flag.Lookup(n.(string))
		if f.DefValue == f.Value.String() {
			log.Stderrf("Required flag not set: -%s", f.Name)
			flag.Usage()
			os.Exit(1)
		}
	}
}

func main() {
	// testBencode()
	// testUPnP()
	flag.Parse()
	log.Stderr("Starting.")
	ts, err := taipei.NewTorrentSession(torrent)
	if err != nil {
		log.Stderr("Could not create torrent session.", err)
		return
	}
	err = ts.DoTorrent()
	if err != nil {
		log.Stderr("Failed: ", err)
	} else {
		log.Stderr("Done")
	}
}
