package main

import (
	"flag"
	"net"
	"net/http"

	"github.com/hailiang/gosocks"
)

func init() {
	flag.StringVar(&proxyAddress, "proxyAddress", "", "Address of a SOCKS5 proxy to use.")
}

var proxyAddress string

func useProxy() bool {
	return len(proxyAddress) > 0
}

func proxyHttpGet(url string) (r *http.Response, e error) {
	return proxyHttpClient().Get(url)
}

func proxyNetDial(netType, addr string) (net.Conn, error) {
	if useProxy() {
		return socks.DialSocksProxy(socks.SOCKS5, proxyAddress)(netType, addr)
	}
	return net.Dial(netType, addr)
}

func proxyHttpClient() (client *http.Client) {
	if useProxy() {
		dialSocksProxy := socks.DialSocksProxy(socks.SOCKS5, proxyAddress)
		tr := &http.Transport{Dial: dialSocksProxy}
		client = &http.Client{Transport: tr}
	} else {
		client = &http.Client{}
	}
	return
}
