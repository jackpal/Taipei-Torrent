package torrent

import (
	"encoding/hex"
	"log"
	"os"
	"os/signal"

	"github.com/nictuku/dht"
	"golang.org/x/net/proxy"
)

type TorrentFlags struct {
	Port                int
	FileDir             string
	SeedRatio           float64
	UseDeadlockDetector bool
	UseLPD              bool
	UseDHT              bool
	UseUPnP             bool
	UseNATPMP           bool
	TrackerlessMode     bool
	ExecOnSeeding       string

	// The dial function to use. Nil means use net.Dial
	Dial proxy.Dialer

	// IP address of gateway used for NAT-PMP
	Gateway string

	//Provides the filesystems added torrents are saved to
	FileSystemProvider FsProvider

	//Whether to check file hashes when adding torrents
	InitialCheck bool

	//Provides cache to each torrent
	Cacher CacheProvider

	//Whether to write and use *.haveBitset resume data
	QuickResume bool

	//How many torrents should be active at a time
	MaxActive int

	//Maximum amount of memory (in MiB) to use for each torrent's Active Pieces.
	//0 means a single Active Piece. Negative means Unlimited Active Pieces.
	MemoryPerTorrent int
}

func RunTorrents(flags *TorrentFlags, torrentFiles []string) (err error) {
	conChan, listenPort, err := ListenForPeerConnections(flags)
	if err != nil {
		log.Println("Couldn't listen for peers connection: ", err)
		return
	}
	quitChan := listenSigInt()

	createChan := make(chan string, flags.MaxActive)
	startChan := make(chan *TorrentSession, 1)
	doneChan := make(chan *TorrentSession, 1)

	var dhtNode dht.DHT
	if flags.UseDHT {
		dhtNode = *startDHT(flags.Port)
	}

	torrentSessions := make(map[string]*TorrentSession)

	go func() {
		for torrentFile := range createChan {
			ts, err := NewTorrentSession(flags, torrentFile, uint16(listenPort))
			if err != nil {
				log.Println("Couldn't create torrent session for "+torrentFile+" .", err)
				doneChan <- &TorrentSession{}
			} else {
				log.Printf("Created torrent session for %s", ts.M.Info.Name)
				startChan <- ts
			}
		}
	}()

	torrentQueue := []string{}
	if len(torrentFiles) > flags.MaxActive {
		torrentQueue = torrentFiles[flags.MaxActive:]
	}

	for i, torrentFile := range torrentFiles {
		if i < flags.MaxActive {
			createChan <- torrentFile
		} else {
			break
		}
	}

	lpd := &Announcer{}
	if flags.UseLPD {
		lpd, err = NewAnnouncer(uint16(listenPort))
		if err != nil {
			log.Println("Couldn't listen for Local Peer Discoveries: ", err)
			flags.UseLPD = false
		}
	}

	theWorldisEnding := false
mainLoop:
	for {
		select {
		case ts := <-startChan:
			if !theWorldisEnding {
				ts.dht = &dhtNode
				if flags.UseLPD {
					lpd.Announce(ts.M.InfoHash)
				}
				torrentSessions[ts.M.InfoHash] = ts
				log.Printf("Starting torrent session for %s", ts.M.Info.Name)
				go func(t *TorrentSession) {
					t.DoTorrent()
					doneChan <- t
				}(ts)
			}
		case ts := <-doneChan:
			if ts.M != nil {
				delete(torrentSessions, ts.M.InfoHash)
				if flags.UseLPD {
					lpd.StopAnnouncing(ts.M.InfoHash)
				}
			}
			if !theWorldisEnding && len(torrentQueue) > 0 {
				createChan <- torrentQueue[0]
				torrentQueue = torrentQueue[1:]
				continue mainLoop
			}

			if len(torrentSessions) == 0 {
				break mainLoop
			}
		case <-quitChan:
			theWorldisEnding = true
			for _, ts := range torrentSessions {
				go ts.Quit()
			}
		case c := <-conChan:
			//	log.Printf("New bt connection for ih %x", c.Infohash)
			if ts, ok := torrentSessions[c.Infohash]; ok {
				ts.AcceptNewPeer(c)
			}
		case dhtPeers := <-dhtNode.PeersRequestResults:
			for key, peers := range dhtPeers {
				if ts, ok := torrentSessions[string(key)]; ok {
					// log.Printf("Received %d DHT peers for torrent session %x\n", len(peers), []byte(key))
					for _, peer := range peers {
						peer = dht.DecodePeerAddress(peer)
						ts.HintNewPeer(peer)
					}
				} else {
					log.Printf("Received DHT peer for an unknown torrent session %x\n", []byte(key))
				}
			}
		case announce := <-lpd.Announces:
			hexhash, err := hex.DecodeString(announce.Infohash)
			if err != nil {
				log.Println("Err with hex-decoding:", err)
			}
			if ts, ok := torrentSessions[string(hexhash)]; ok {
				// log.Printf("Received LPD announce for ih %s", announce.Infohash)
				ts.HintNewPeer(announce.Peer)
			}
		}
	}
	if flags.UseDHT {
		dhtNode.Stop()
	}
	return
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}

func startDHT(listenPort int) *dht.DHT {
	// TODO: UPnP UDP port mapping.
	cfg := dht.NewConfig()
	cfg.Port = listenPort
	cfg.NumTargetPeers = TARGET_NUM_PEERS
	dhtnode, err := dht.New(cfg)
	if err != nil {
		log.Println("DHT node creation error:", err)
		return nil
	}

	go dhtnode.Run()

	return dhtnode
}
