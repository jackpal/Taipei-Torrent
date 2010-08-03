include $(GOROOT)/src/Make.$(GOARCH)

all : taipeitorrent

TARG=taipeitorrent
GOFILES=main.go

include $(GOROOT)/src/Make.cmd
