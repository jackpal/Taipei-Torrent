include $(GOROOT)/src/Make.$(GOARCH)

all : taipeitorrent

TARG=taipeitorrent

GOFILES=\
	bitset.go \
	files.go \
	main.go \
	metainfo.go \
	testBencode.go \
	upnp.go

include $(GOROOT)/src/Make.cmd
