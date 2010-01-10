include $(GOROOT)/src/Make.$(GOARCH)

TARG=taipeitorrent

GOFILES=\
	files.go \
	main.go \
	metainfo.go \
	testBencode.go \
	upnp.go

include $(GOROOT)/src/Make.cmd