package torrent

import (
	"fmt"
	"log"
	"net"
	"strconv"
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
		log.Printf("Listener failed while finding the host/port for %v: %v", portstring, err)
		return
	}
	go func() {
		for {
			var conn net.Conn
			conn, err := listener.Accept()
			if err != nil {
				// https://code.google.com/p/go/issues/detail?id=4373
				if err.Error() == "use of closed network connection" {
					return
				}
				log.Println("Listener accept failed:", err)
				continue
			}
			header, err := readHeader(conn)
			if err != nil {
				log.Println("Error reading header: ", err)
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
	nat, err := CreatePortMapping(flags)
	if err != nil {
		err = fmt.Errorf("Unable to create NAT: %v", err)
		return
	}
	listenPort := flags.Port
	if nat != nil {
		var external net.IP
		if external, err = nat.GetExternalAddress(); err != nil {
			err = fmt.Errorf("Unable to get external IP address from NAT: %v", err)
			return
		}
		log.Println("External ip address: ", external)
		if listenPort, err = chooseListenPort(nat, listenPort); err != nil {
			log.Println("Could not choose listen port.", err)
			log.Println("Peer connectivity will be affected.")
		}
	}
	listener, err = net.ListenTCP("tcp", &net.TCPAddr{Port: listenPort})
	if err != nil {
		log.Fatal("Listen failed:", err)
	}
	log.Println("Listening for peers on port:", listenPort)
	externalPort = listenPort
	return
}

// createPortMapping creates a NAT port mapping, or nil if none requested or found.
func CreatePortMapping(flags *TorrentFlags) (nat NAT, err error) {
	if flags.UseUPnP && flags.UseNATPMP {
		err = fmt.Errorf("Cannot specify both -useUPnP and -useNATPMP")
		return
	}
	if flags.UseUPnP {
		log.Println("Using UPnP to open port.")
		nat, err = Discover()
	}
	if flags.UseNATPMP {
		if flags.Gateway == "" {
			err = fmt.Errorf("useNATPMP requires gateway")
			return
		}
		log.Println("Using NAT-PMP to open port.")
		gatewayIP := net.ParseIP(flags.Gateway)
		if gatewayIP == nil {
			err = fmt.Errorf("Could not parse gateway %q", flags.Gateway)
		}
		nat = NewNatPMP(gatewayIP)
	}
	return
}

func chooseListenPort(nat NAT, externalPort int) (listenPort int, err error) {
	// TODO: Unmap port when exiting. (Right now we never exit cleanly.)
	// TODO: Defend the port, remap when router reboots
	listenPort, err = nat.AddPortMapping("tcp", externalPort, externalPort,
		"Taipei-Torrent port "+strconv.Itoa(externalPort), 360000)
	if err != nil {
		return
	}
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
