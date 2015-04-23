package torrent

import (
	"encoding/hex"
	"github.com/nictuku/dht"
	"golang.org/x/net/proxy"
	"log"
	"time"
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
}

func RunTorrents(flags *TorrentFlags, torrentFiles []string) (err error) {
	err, commandChan, listenPort := RunDaemon(flags)
	if err != nil {
		log.Println("Couldn't Run TaipeiTorrent: ", err)
		return
	}

	replyChan := make(chan *TTCreply, 16)
	for _, tFile := range torrentFiles {
		ts, err := NewTorrentSession(flags, tFile, uint16(listenPort))
		if err != nil {
			log.Println("Could not create torrent session.", err)
		}
		commandChan <- &TTCaddTorrent{ts, replyChan}
		reply := <-replyChan
		if reply.Err != nil {
			log.Println("Error adding Torrent: ", reply.Err)
		}
	}

	for {
		commandChan <- &TTCgetTorrentsActive{replyChan}
		reply := <-replyChan
		if reply.Err != nil {
			log.Println("Error getting active Torrents: ", reply.Err)
			return
		}
		listActive := reply.V.([]string)
		if len(listActive) == 0 {
			return
		}
		time.Sleep(10 * time.Second)
	}
	return nil
}

func RunDaemon(flags *TorrentFlags) (err error, commandChan chan TTCommand, listenPort int) {
	var conChan chan *BtConn
	conChan, listenPort, err = ListenForPeerConnections(flags)
	if err != nil {
		log.Println("Couldn't listen for peers connection: ", err)
		return
	}

	commandChan = make(chan TTCommand, 16)
	go runMainLoop(flags, conChan, commandChan, listenPort)
	return
}

func runMainLoop(flags *TorrentFlags, conChan chan *BtConn, commandChan chan TTCommand, listenPort int) {

	doneChan := make(chan *TorrentSession, 1)

	var dhtNode dht.DHT
	if flags.UseDHT {
		dhtNode = *startDHT(flags.Port)
	}

	torrentSessions := make(map[string]*TorrentSession)

	torrentQueue := []*TorrentSession{}
	var err error

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
		case command := <-commandChan:
			switch command := command.(type) {
			case *TTCaddTorrent:
				torrentQueue = append(torrentQueue, command.NewTS)
				if len(torrentSessions) < flags.MaxActive {
					doneChan <- nil
				}
				command.ReplyChan <- &TTCreply{nil, nil}
			case *TTCgetTorrentsActive:
				active := []string{}
				for k, _ := range torrentSessions {
					active = append(active, k)
				}
				command.ReplyChan <- &TTCreply{nil, active}
			case *TTCquit:
				theWorldisEnding = true
				if len(torrentSessions) == 0 {
					break mainLoop
				}
				for _, ts := range torrentSessions {
					go ts.Quit()
				}
			}

		case ts := <-doneChan:
			if ts != nil {
				delete(torrentSessions, ts.M.InfoHash)
				if flags.UseLPD {
					lpd.StopAnnouncing(ts.M.InfoHash)
				}
			}

			if !theWorldisEnding && len(torrentQueue) > 0 && len(torrentSessions) < flags.MaxActive {
				newTS := torrentQueue[0]
				torrentQueue = torrentQueue[1:]
				newTS.dht = &dhtNode
				if flags.UseLPD {
					lpd.Announce(newTS.M.InfoHash)
				}
				torrentSessions[newTS.M.InfoHash] = newTS
				log.Printf("Starting torrent session for %s", newTS.M.Info.Name)
				go func(t *TorrentSession) {
					t.DoTorrent()
					doneChan <- t
				}(newTS)
			}
			if theWorldisEnding && len(torrentSessions) == 0 {
				break mainLoop
			}
		case c := <-conChan:
			log.Printf("New bt connection for ih %x", c.Infohash)
			if ts, ok := torrentSessions[c.Infohash]; ok {
				ts.AcceptNewPeer(c)
			}
		case dhtPeers := <-dhtNode.PeersRequestResults:
			for key, peers := range dhtPeers {
				if ts, ok := torrentSessions[string(key)]; ok {
					log.Printf("Received %d DHT peers for torrent session %x\n", len(peers), []byte(key))
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
				log.Printf("Received LPD announce for ih %s", announce.Infohash)
				ts.HintNewPeer(announce.Peer)
			}
		}
	}
	if flags.UseDHT {
		dhtNode.Stop()
	}
	return
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
