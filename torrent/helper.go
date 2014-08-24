package torrent

import (
	"encoding/hex"
	"io/ioutil"
	"log"
	"math"
	"net"
	"os"
	"time"
)

type TorrentClient struct {
	Flags      *TorrentFlags
	ListenPort int
	DoneConn   chan *TorrentSession
	Sessions   map[string]*TorrentSession
	Announcer  *Announcer
	quitChan   chan bool
	listener   net.Listener
}

func NewTorrentClientDefault() *TorrentClient {
	return NewTorrentClient("./data/", 8000)
}

func NewTorrentClient(dir string, port int) *TorrentClient {
	flags := &TorrentFlags{
		Port:      port,
		FileDir:   dir,
		SeedRatio: math.Inf(0),
	}

	newConn, listener, listenPort, err := ListenForPeerConnections(flags)
	if err != nil {
		panic(err)
	}

	announcer, err := NewAnnouncer(uint16(listenPort))
	if err != nil {
		panic(err)
	}

	tc := &TorrentClient{
		DoneConn:   make(chan *TorrentSession),
		Sessions:   make(map[string]*TorrentSession),
		Flags:      flags,
		listener:   listener,
		ListenPort: listenPort,
		Announcer:  announcer,
		quitChan:   make(chan bool),
	}

	go func() {
		for {
			select {
			case ts := <-tc.DoneConn:
				delete(tc.Sessions, ts.M.InfoHash)
				if len(tc.Sessions) == 0 {
					// break mainLoop
					// TODO is this OK??
					// close(tc.quitChan)
					// return
				}
			case <-tc.quitChan:
				log.Println("terminate client main loop")
				return
			case c := <-newConn:
				log.Printf("New bt connection for ih %x", c.Infohash)
				if ts, ok := tc.Sessions[c.Infohash]; ok {
					ts.AcceptNewPeer(c)
				}
			case announce := <-announcer.Announces:
				hexhash, err := hex.DecodeString(announce.Infohash)
				if err != nil {
					log.Println("Err with hex-decoding:", err)
					break
				}
				if ts, ok := tc.Sessions[string(hexhash)]; ok {
					log.Printf("Received LPD announce for ih %s", announce.Infohash)
					ts.HintNewPeer(announce.Peer)
				}
			}
		}
	}()

	return tc
}

func (c *TorrentClient) Download(meta *MetaInfo) *TorrentSession {
	file := SaveTorrent(meta)
	session := c.Serve(file)
	// TODO : add timeout, or progress heatbeat
	for waiting := true; waiting == true; {
		if session.Done == nil {
			// TODO ... uhm should we maybe wait longer?!
			time.Sleep(time.Millisecond * 50)
			continue
		}
		select {
		case <-session.Done:
			waiting = false
			// TODO we might want to unserve at somepoint ...
			//c.Unserve(session)
		}
	}
	return session
}

func (c *TorrentClient) Unserve(session *TorrentSession) {
	c.Announcer.StopAnnouncing(session.M.InfoHash)
	err := session.Quit()
	if err != nil {
		panic(err)
	}
}

func (c *TorrentClient) Serve(torrentfile string) *TorrentSession {
	session, err := NewTorrentSession(c.Flags, torrentfile, uint16(c.ListenPort))
	if err != nil {
		log.Println("Could not create torrent session.", err)
		panic(err)
	}

	// fmt.Printf("Starting torrent session for %x\n", session.M.InfoHash)
	c.Sessions[session.M.InfoHash] = session

	c.Announcer.Announce(session.M.InfoHash)

	go func() {
		session.DoTorrent()
		c.DoneConn <- session
	}()
	return session
}

func (c *TorrentClient) Shutdown() {
	c.listener.Close()
	defer func() {
		recover()
	}()
	c.quitChan <- true
}

func SaveTorrent(meta *MetaInfo) string {
	var torrentFile *os.File
	torrentFile, err := ioutil.TempFile("", "torrent")
	if err != nil {
		panic(err)
	}
	defer torrentFile.Close()

	err = meta.Bencode(torrentFile)
	if err != nil {
		panic(err)
	}
	return torrentFile.Name()
}

func NewTorrentFor(filename string) (*MetaInfo, string) {
	var metaInfo *MetaInfo
	metaInfo, err := CreateMetaInfoFromFileSystem(nil, filename, 0, false)
	if err != nil {
		panic(err)
	}
	metaInfo.Announce = "http://127.0.0.1:8085/announce"
	metaInfo.CreatedBy = "remerge"
	torrentFile := SaveTorrent(metaInfo)
	return metaInfo, torrentFile
}
