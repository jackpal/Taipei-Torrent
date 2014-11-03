package torrent

import (
	"fmt"
	"net"
	"regexp"
)

// btConn wraps an incoming network connection and contains metadata that helps
// identify which active torrentSession it's relevant for.
type btConn struct {
	conn     net.Conn
	header   []byte
	Infohash string
	id       string
}

// listenForPeerConnections listens on a TCP port for incoming connections and
// demuxes them to the appropriate active torrentSession based on the InfoHash
// in the header.
func ListenForPeerConnections(flags *TorrentFlags) (conChan chan *btConn, listener net.Listener, listenPort int, err error) {
	listener, listenPort, err = CreateListener(flags)
	if err != nil {
		return
	}
	conChan = make(chan *btConn)
	_, portstring, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Infof("Listener failed while finding the host/port for %v: %v", portstring, err)
		return
	}
	go func() {
		for {
			var conn net.Conn
			conn, err := listener.Accept()
			if err != nil {
				// https://code.google.com/p/go/issues/detail?id=4373
				if matched, _ := regexp.MatchString("use of closed network connection", err.Error()); matched {
					return
				}
				logPrintln("Listener accept failed:", err)
				continue
			}
			header, err := readHeader(conn)
			if err != nil {
				logPrintln("Error reading header: ", err)
				continue
			}
			peersInfoHash := string(header[8:28])
			id := string(header[28:48])
			conChan <- &btConn{
				header:   header,
				Infohash: peersInfoHash,
				id:       id,
				conn:     conn,
			}
		}
	}()
	return
}

func CreateListener(flags *TorrentFlags) (listener net.Listener, externalPort int, err error) {
	listenPort := flags.Port
	listener, err = net.ListenTCP("tcp", &net.TCPAddr{Port: listenPort})
	if err != nil {
		panic(err)
	}
	listenPort = listener.Addr().(*net.TCPAddr).Port
	log.Debugf("listening for peers on port %d", listenPort)
	externalPort = listenPort
	return
}

func readHeader(conn net.Conn) (h []byte, err error) {
	header := make([]byte, 68)
	_, err = conn.Read(header[0:1])
	if err != nil {
		err = fmt.Errorf("Couldn't read 1st byte: %v", err)
		return
	}
	if header[0] != 19 {
		err = fmt.Errorf("First byte is not 19")
		return
	}
	_, err = conn.Read(header[1:20])
	if err != nil {
		err = fmt.Errorf("Couldn't read magic string: %v", err)
		return
	}
	if string(header[1:20]) != "BitTorrent protocol" {
		err = fmt.Errorf("Magic string is not correct: %v", string(header[1:20]))
		return
	}
	// Read rest of header
	_, err = conn.Read(header[20:])
	if err != nil {
		err = fmt.Errorf("Couldn't read rest of header")
		return
	}

	h = make([]byte, 48)
	copy(h, header[20:])
	return
}
