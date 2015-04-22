// torrentControl.go
package torrent

import ()

/*
 My proposal to solve the gui crisis. The idea is that anything that wants to
 interact with TaipeiTorrent can do so through the (as yet not completely implemented)
 TorrentControl object. This includes any sortof webgui, native gui, RPC client,
 or something that polls a specified directory and runs any new .torrent or .magnet
 files that get placed there.

 Part of this proposal requires the Chunk struct to be made public.
*/

type TorrentController interface {
	GetFlags() TorrentFlags
	GetNat() (NAT, error)
	GetTaipeiStatus() map[string]string
	GetTorrentsActive() []string
	GetTorrentsQueued() []string
	AddTorrent(torr string, start bool) (string, error)

	GetTorrentStatus(infohash string) (map[string]string, error)
	StartTorrent(infohash string) error
	PauseTorrent(infohash string) error
	StopTorrent(infohash string) error
	RemoveTorrent(infohash string) error

	PutMetaData(infohash string, meta MetaInfo) error
	GetMetaData(infohash string) (MetaInfo, error)
	GetPiecesHave(infohash string) (Bitset, error)
	GetPiecesWant(infohash string) (Bitset, error)
	SetPiecesWant(infohash string, wanted Bitset) error
	GetPieces(infohash string, wanted Bitset) ([]Chunk, error)
	PutPieces(infohash string, pieces []Chunk) error
	GetPeers(infohash string) ([]string, error)
	AddPeers(infohash string, peers []string) error

	Close() error
}

type TorrentControl struct{}
