package torrent

import (
	"golang.org/x/net/proxy"
	"net"
	"net/http"
)

func proxyNetDial(dialer proxy.Dialer, network, address string) (net.Conn, error) {
	if dialer != nil {
		return dialer.Dial(network, address)
	}
	return net.Dial(network, address)
}

func proxyHttpGet(dialer proxy.Dialer, url string) (r *http.Response, e error) {
	return proxyHttpClient(dialer).Get(url)
}

func proxyHttpClient(dialer proxy.Dialer) (client *http.Client) {
	if dialer == nil {
		dialer = proxy.Direct
	}
	tr := &http.Transport{Dial: dialer.Dial}
	client = &http.Client{Transport: tr}
	return
}
