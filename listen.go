package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
)

var (
	// If the port is 0, picks up a random port - but the DHT will keep
	// running on port 0 because ListenUDP doesn't do that.
	// Don't use port 6881 which blacklisted by some trackers.
	port      = flag.Int("port", 7777, "Port to listen on.")
	useUPnP   = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")
	useNATPMP = flag.Bool("useNATPMP", false, "Use NAT-PMP to open port in firewall.")
)

type btConn struct {
	conn     net.Conn
	header   []byte
	infohash string
	id       string
}

func listenForPeerConnections() (conChan chan *btConn, listenPort int, err error) {

	listener, err := getListener()
	if err != nil {
		return
	}

	conChan = make(chan *btConn)

	_, portstring, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return
	}
	listenPort, err = strconv.Atoi(portstring)
	if err != nil {
		return
	}

	go func() {
		for {
			var conn net.Conn
			conn, err := listener.Accept()
			if err != nil {
				log.Println("Listener failed:", err)
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

func getListener() (listener net.Listener, err error) {
	var listenPort int
	nat, err := createNAT()

	if err != nil {
		log.Println("Unable to create NAT:", err)
		return
	}
	if nat == nil {
		listenPort = *port
	} else {
		var external net.IP
		external, err = nat.GetExternalAddress()
		if err != nil {
			log.Println("Unable to get external IP address from NAT")
			return
		}
		log.Println("External ip address: ", external)
		if listenPort, err = chooseListenPort(nat); err != nil {
			log.Println("Could not choose listen port.", err)
			log.Println("Peer connectivity will be affected.")
		}
	}

	listenString := ":" + strconv.Itoa(listenPort)
	listener, err = net.Listen("tcp", listenString)
	if err != nil {
		log.Fatal("Listen failed:", err)
	}

	log.Println("Listening for peers on port:", listenPort)

	return
}

// Create a NAT, or nil if none requested or found.
func createNAT() (nat NAT, err error) {
	if *useUPnP && *useNATPMP {
		err = errors.New("Cannot specify both -useUPnP and -useNATPMP")
		return
	}
	if *useNATPMP {
		if gateway == "" {
			err = errors.New("-useNATPMP requires -gateway")
			return
		}
	}
	if *useUPnP {
		log.Println("Using UPnP to open port.")
		nat, err = Discover()
	}
	if *useNATPMP {
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

func readHeader(conn net.Conn) (header []byte, err error) {
	header = make([]byte, 68)
	_, err = conn.Read(header[0:1])
	if err != nil {
		log.Println("Couldn't read 1st byte")
		return
	}
	if header[0] != 19 {
		log.Println("First byte is not 19")
		return
	}
	_, err = conn.Read(header[1:20])
	if err != nil {
		log.Println("Couldn't read magic string")
		return
	}
	if string(header[1:20]) != "BitTorrent protocol" {
		log.Println("Magic string is not correct: ", string(header[1:20]))
		return
	}
	// Read rest of header
	_, err = conn.Read(header[20:])
	if err != nil {
		log.Println("Couldn't read rest of header")
		return
	}

	return
}
