package main

import (
	"flag"
	"log"
)

var torrent *string = flag.String("torrent", "", "URL or path to a torrent file")
var fileDir *string = flag.String("fileDir", "", "path to directory where files are stored")
var debugp *bool = flag.Bool("debug", false, "Turn on debugging")
var port *int = flag.Int("port", 0, "Port to listen on. Defaults to random.")
var useUPnP *bool = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")

func main() {
	// testBencode()
	// testUPnP()
	flag.Parse()
	log.Stderr("Starting.")
	listenPort, err := chooseListenPort()
	if err != nil {
		log.Stderr("Could not choose listen port.")
		return
	}
	err = doTorrent(listenPort)
	if err != nil {
		log.Stderr("Failed: ", err)
	} else {
		log.Stderr("Done")
	}
}
