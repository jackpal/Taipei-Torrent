package torrent

import ()

type TTCommand interface {
}

type TTCreply struct {
	Err error
	V   interface{}
}

type TTCaddTorrent struct {
	NewTS     *TorrentSession
	ReplyChan chan *TTCreply
}

type TTCgetTorrentsActive struct {
	ReplyChan chan *TTCreply
}

type TTCquit struct {
	ReplyChan chan *TTCreply
}
