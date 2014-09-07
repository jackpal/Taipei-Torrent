package torrent

import (
	"net"
	"net/http"
)

func proxyNetDial(dialer Dialer, network, address string) (net.Conn, error) {
	if dialer != nil {
		return dialer(network, address)
	}
	return net.Dial(network, address)
}

func proxyHttpGet(dialer Dialer, url string) (r *http.Response, e error) {
	return proxyHttpClient(dialer).Get(url)
}

func proxyHttpClient(dialer Dialer) (client *http.Client) {
	tr := &http.Transport{Dial: dialer}
	client = &http.Client{Transport: tr}
	return
}
