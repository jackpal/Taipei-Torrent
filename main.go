package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
)

func peerId() string {
	sid := "gotor_" + strconv.Itoa(os.Getpid()) + "______________"
	return sid[0:20]
}

func binaryToDottedPort(port string) string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", port[0], port[1], port[2], port[3],
		(port[4]<<8)|port[5])
}

func main() {
	log.Stderr("Starting.")
	// testBencode()
	m, err := getMetaInfo("http://releases.ubuntu.com/9.10/ubuntu-9.10-desktop-amd64.iso.torrent")
	if err != nil {
		log.Stderr(err)
		return
	}
	log.Stderr("Tracker: ", m.Announce, " Comment: ", m.Comment, " Encoding: ", m.Encoding)
	// log.Stderr("Info: ", m.Info, " InfoHash:", m.InfoHash)

	si := &SessionInfo{PeerId: peerId(), Port: 6881, Left: 12}

	tr, err := getTrackerInfo(m, si)
	if err != nil {
		log.Stderr(err)
		return
	}

	ip := tr.Peers[0:6]
	c, err := net.Dial("tcp", "", binaryToDottedPort(ip))
	if err != nil {
		log.Stderr(err)
		return
	}
	var header [28]byte
	_, err = c.Read(&header)
	if err != nil {
		log.Stderr(err)
		return
	}
	log.Stderr(header[1:20])
	log.Stderr(tr)
}

