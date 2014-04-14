package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

var useLPD = flag.Bool("useLPD", false, "Use Local Peer Discovery")
var (
	request_template = "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: 239.192.152.143:6771\r\n" +
		"Port: %d\r\n" +
		"Infohash: %X\r\n\r\n"
)

type Announce struct {
	peer     string
	infohash string
}

type Announcer struct {
	btPort int
	addr   *net.UDPAddr
	conn   *net.UDPConn

	announces       chan *Announce
	activeAnnounces map[string]*time.Ticker
}

func NewAnnouncer(listenPort int) (lpd *Announcer, err error) {
	addr, err := net.ResolveUDPAddr("udp4", "239.192.152.143:6771")
	if err != nil {
		return
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return
	}

	activeAnnounces := make(map[string]*time.Ticker)
	lpd = &Announcer{
		btPort:          listenPort,
		addr:            addr,
		conn:            conn,
		announces:       make(chan *Announce),
		activeAnnounces: activeAnnounces,
	}

	go lpd.run()
	return
}

func (lpd *Announcer) run() {
	for {
		answer := make([]byte, 256)
		_, from, err := lpd.conn.ReadFromUDP(answer)
		if err != nil {
			log.Println("Error reading from UDP: ", err)
			continue
		}

		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(answer)))
		if err != nil {
			log.Println("Error reading HTTP request from UDP: ", err)
			continue
		}

		if req.Method != "BT-SEARCH" {
			log.Println("Invalid method: ", req.Method)
		}

		ih := req.Header.Get("Infohash")
		if ih == "" {
			log.Println("No Infohash")
			continue
		}

		port := req.Header.Get("Port")
		if port == "" {
			log.Println("No port")
			continue
		}

		addr, err := net.ResolveTCPAddr("tcp4", from.IP.String()+":"+port)
		if err != nil {
			log.Println(err)
			continue
		}
		lpd.announces <- &Announce{addr.String(), ih}
	}
}

func (lpd *Announcer) Announce(ih string) {
	go func() {
		requestMessage := []byte(fmt.Sprintf(request_template, lpd.btPort,
			ih))

		// Announce at launch, then every 5 minutes
		_, err := lpd.conn.WriteToUDP(requestMessage, lpd.addr)
		if err != nil {
			log.Println(err)
		}

		ticker := time.NewTicker(5 * time.Minute)
		lpd.activeAnnounces[ih] = ticker

		for _ = range ticker.C {
			_, err := lpd.conn.WriteToUDP(requestMessage, lpd.addr)
			if err != nil {
				log.Println(err)
			}
		}
	}()
}

func (lpd *Announcer) StopAnnouncing(ih string) {
	if ticker, ok := lpd.activeAnnounces[ih]; ok {
		ticker.Stop()
		delete(lpd.activeAnnounces, ih)
	}
}
