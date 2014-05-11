package main

import (
	"encoding/hex"
	"flag"
	"github.com/jackpal/Taipei-Torrent/torrent"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
)

var (
	cpuprofile    = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile    = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
	useLPD        = flag.Bool("useLPD", false, "Use Local Peer Discovery")
	createTorrent = flag.String("createTorrent", "", "If not empty, creates a torrent file from the given path. Writes to stdout")
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if *createTorrent != "" {
		err := torrent.WriteMetaInfoBytes(*createTorrent, os.Stdout)
		if err != nil {
			log.Fatal("Could not create torrent file: ", err)
		}
		return
	}

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

	if *cpuprofile != "" {
		cpuf, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(cpuf)
		defer pprof.StopCPUProfile()
	}

	if *memprofile != "" {
		defer func(file string) {
			memf, err := os.Create(file)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(memf)
		}(*memprofile)
	}

	log.Println("Starting.")

	conChan, listenPort, err := torrent.ListenForPeerConnections()
	if err != nil {
		log.Fatal("Couldn't listen for peers connection: ", err)
	}
	quitChan := listenSigInt()

	doneChan := make(chan *torrent.TorrentSession)

	torrentFile := args[0]
	ts, err := torrent.NewTorrentSession(torrentFile, listenPort)
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return
	}
	torrentSessions := make(map[string]*torrent.TorrentSession)
	torrentSessions[ts.M.InfoHash] = ts

	log.Printf("Starting torrent session for %x", ts.M.InfoHash)

	for _, ts := range torrentSessions {
		go func() {
			ts.DoTorrent()
			doneChan <- ts
		}()
	}

	lpd := &torrent.Announcer{}
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

}

func usage() {
	log.Printf("usage: torrent.Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}

func startLPD(torrentSessions map[string]*torrent.TorrentSession, listenPort uint16) (lpd *torrent.Announcer) {
	lpd, err := torrent.NewAnnouncer(listenPort)
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
