include $(GOROOT)/src/Make.$(GOARCH)

all : taipeitorrent

TARG=taipeitorrent

GOFILES=\
	bitset.go \
	files.go \
	main.go \
	metainfo.go \
	peer.go \
	testBencode.go \
	torrent.go \
	upnp.go

include $(GOROOT)/src/Make.cmd
