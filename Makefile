PACKAGE := github.com/remerge/torrent

# http://stackoverflow.com/questions/322936/common-gnu-makefile-directory-path#comment11704496_324782
TOP := $(dir $(CURDIR)/$(word $(words $(MAKEFILE_LIST)),$(MAKEFILE_LIST)))

export GOPATH := $(TOP)/.godeps
export PATH := $(TOP)/.godeps/bin:$(PATH)

# Go parameters
GO=go
GOBUILD=$(GO) build
GOCLEAN=$(GO) clean
GOINSTALL=$(GO) install
GOTEST=$(GO) test
GOFMT=gofmt -w

SRCS=$(wildcard main/*.go)
OBJS=$(patsubst main/%.go,%,$(SRCS))

.PHONY: build clean install test iref fmt gen

all: build

build: fmt
	$(GOBUILD) $(SRCS)

clean:
	$(GOCLEAN)
	rm -f $(OBJS)

test:
	$(GOTEST)

fmt:
	$(GOFMT) .

dep:
	@curl -s -L -k http://git.io/0dFd8Q | bash -s ${PACKAGE}
