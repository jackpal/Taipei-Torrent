package torrent

import (
	"encoding/hex"
	"flag"
	"log"
	"os"
	"os/signal"
)

var (
	useLPD = flag.Bool("useLPD", false, "Use Local Peer Discovery")
)

func RunTorrents(torrentFiles []string) (err error) {
	conChan, listenPort, err := ListenForPeerConnections()
	if err != nil {
		log.Println("Couldn't listen for peers connection: ", err)
		return
	}
	quitChan := listenSigInt()

	doneChan := make(chan *TorrentSession)

	torrentSessions := make(map[string]*TorrentSession)

	for _, torrentFile := range torrentFiles {
		var ts *TorrentSession
		ts, err = NewTorrentSession(torrentFile, listenPort)
		if err != nil {
			log.Println("Could not create torrent session.", err)
			return
		}
		log.Printf("Starting torrent session for %x", ts.M.InfoHash)
		torrentSessions[ts.M.InfoHash] = ts
	}

	for _, ts := range torrentSessions {
		go func(ts *TorrentSession) {
			ts.DoTorrent()
			doneChan <- ts
		}(ts)
	}

	lpd := &Announcer{}
	if *useLPD {
		lpd = startLPD(torrentSessions, listenPort)
	}

mainLoop:
	for {
		select {
		case ts := <-doneChan:
			delete(torrentSessions, ts.M.InfoHash)
			if len(torrentSessions) == 0 {
				break mainLoop
			}
		case <-quitChan:
			for _, ts := range torrentSessions {
				err := ts.Quit()
				if err != nil {
					log.Println("Failed: ", err)
				} else {
					log.Println("Done")
				}
			}
		case c := <-conChan:
			log.Printf("New bt connection for ih %x", c.Infohash)
			if ts, ok := torrentSessions[c.Infohash]; ok {
				ts.AcceptNewPeer(c)
			}
		case announce := <-lpd.Announces:
			hexhash, err := hex.DecodeString(announce.Infohash)
			if err != nil {
				log.Println("Err with hex-decoding:", err)
				break
			}
			if ts, ok := torrentSessions[string(hexhash)]; ok {
				log.Printf("Received LPD announce for ih %s", announce.Infohash)
				ts.HintNewPeer(announce.Peer)
			}
		}
	}
	return
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}

func startLPD(torrentSessions map[string]*TorrentSession, listenPort uint16) (lpd *Announcer) {
	lpd, err := NewAnnouncer(listenPort)
	if err != nil {
		log.Println("Couldn't listen for Local Peer Discoveries: ", err)
		return
	} else {
		for _, ts := range torrentSessions {
			lpd.Announce(ts.M.InfoHash)
		}
	}
	return
}
