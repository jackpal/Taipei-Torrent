package torrent

import (
	"net"
	"os"
	"os/signal"
)

type Dialer func(network, addr string) (net.Conn, error)

type TorrentFlags struct {
	Port                int
	FileDir             string
	SeedRatio           float64
	UseDeadlockDetector bool
	UseLPD              bool
	TrackerlessMode     bool

	// The dial function to use. Nil means use net.Dial
	Dial Dialer
}

func RunTorrents(flags *TorrentFlags, torrentFiles []string) (err error) {
	conChan, _, listenPort, err := ListenForPeerConnections(flags)
	if err != nil {
		logPrintln("Couldn't listen for peers connection: ", err)
		return
	}
	quitChan := listenSigInt()

	doneChan := make(chan *TorrentSession)

	torrentSessions := make(map[string]*TorrentSession)

	for _, torrentFile := range torrentFiles {
		var ts *TorrentSession
		ts, err = NewTorrentSession(flags, torrentFile, uint16(listenPort))
		if err != nil {
			logPrintln("Could not create torrent session.", err)
			return
		}
		log.Infof("Starting torrent session for %x", ts.M.InfoHash)
		torrentSessions[ts.M.InfoHash] = ts
	}

	for _, ts := range torrentSessions {
		go func(ts *TorrentSession) {
			ts.DoTorrent()
			doneChan <- ts
		}(ts)
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
					logPrintln("Failed: ", err)
				} else {
					logPrintln("Done")
				}
			}
		case c := <-conChan:
			log.Infof("New bt connection for ih %x", c.Infohash)
			if ts, ok := torrentSessions[c.Infohash]; ok {
				ts.AcceptNewPeer(c)
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
