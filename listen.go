package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
)

var (
	// If the port is 0, picks up a random port. Don't use port 6881 which
	// is blacklisted by some trackers.
	port      = flag.Int("port", 7777, "Port to listen on.")
	useUPnP   = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")
	useNATPMP = flag.Bool("useNATPMP", false, "Use NAT-PMP to open port in firewall.")
)

// btConn wraps an incoming network connection and contains metadata that helps
// identify which active torrentSession it's relevant for.
type btConn struct {
	conn     net.Conn
	header   []byte
	infohash string
	id       string
}

// listenForPeerConnections listens on a TCP port for incoming connections and
// demuxes them to the appropriate active torrentSession based on the InfoHash
// in the header.
func listenForPeerConnections() (conChan chan *btConn, listenPort int, err error) {
	listener, err := createListener()
	if err != nil {
		return
	}
	conChan = make(chan *btConn)
	_, portstring, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Printf("Listener failed while finding the host/port for %v: %v", portstring, err)
		return
	}
	listenPort, err = strconv.Atoi(portstring)
	if err != nil {
		log.Printf("Listener failed while converting %v to integer: %v", portstring, err)
		return
	}
	go func() {
		for {
			var conn net.Conn
			conn, err := listener.Accept()
			if err != nil {
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
				infohash: peersInfoHash,
				id:       id,
				conn:     conn,
			}
		}
	}()
	return
}

func createListener() (listener net.Listener, err error) {
	nat, err := createPortMapping()
	if err != nil {
		err = fmt.Errorf("Unable to create NAT: %v", err)
		return
	}
	listenPort := *port
	if nat != nil {
		var external net.IP
		if external, err = nat.GetExternalAddress(); err != nil {
			err = fmt.Errorf("Unable to get external IP address from NAT: %v", err)
			return
		}
		log.Println("External ip address: ", external)
		if listenPort, err = chooseListenPort(nat); err != nil {
			log.Println("Could not choose listen port.", err)
			log.Println("Peer connectivity will be affected.")
		}
	}
	listener, err = net.ListenTCP("tcp", &net.TCPAddr{Port: listenPort})
	if err != nil {
		log.Fatal("Listen failed:", err)
	}
	log.Println("Listening for peers on port:", listenPort)
	return
}

// createPortMapping creates a NAT port mapping, or nil if none requested or found.
func createPortMapping() (nat NAT, err error) {
	if *useUPnP && *useNATPMP {
		err = fmt.Errorf("Cannot specify both -useUPnP and -useNATPMP")
		return
	}
	if *useUPnP {
		log.Println("Using UPnP to open port.")
		nat, err = Discover()
	}
	if *useNATPMP {
		if gateway == "" {
			err = fmt.Errorf("-useNATPMP requires -gateway")
			return
		}
		log.Println("Using NAT-PMP to open port.")
		gatewayIP := net.ParseIP(gateway)
		if gatewayIP == nil {
			err = fmt.Errorf("Could not parse gateway %q", gateway)
		}
		nat = NewNatPMP(gatewayIP)
	}
	return
}

func chooseListenPort(nat NAT) (listenPort int, err error) {
	listenPort = *port
	// TODO: Unmap port when exiting. (Right now we never exit cleanly.)
	// TODO: Defend the port, remap when router reboots
	listenPort, err = nat.AddPortMapping("tcp", listenPort, listenPort,
		"Taipei-Torrent port "+strconv.Itoa(listenPort), 360000)
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
