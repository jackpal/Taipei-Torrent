include $(GOROOT)/src/Make.$(GOARCH)

TARG=taipeitorrent

GOFILES=\
	main.go \
	metainfo.go \
	files.go \
	testBencode.go

include $(GOROOT)/src/Make.cmd