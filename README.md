Taipei Torrent
==============

This is a simple command-line-interface BitTorrent client coded in the go
programming language.

FAQ:
----

Q: Why call it Taipei Torrent?

A: I (jackpal) started writing it while visiting Taipei, Taiwan

Q: What is the license?

A: See the LICENSE file.

Current Status
--------------

Tested on Windows, Linux and Mac OSX.

Development Roadmap
-------------------

+  Implement choke/unchoke logic
+  Full UPnP support (need to be able to search for an unused listener port,
   detect we have already acquired the port,
   release the listener port when we quit.)
+  Clean up source code
+  Deal with TODOs
+  Add a way of quitting other than typing control-C

Download, Install, and Build Instructions
-----------------------------------------

1. Download and install the Go tools from http://golang.org

2. Use the "go" command to download, install, and build the Taipei-Torrent
app:

    go get github.com/jackpal/Taipei-Torrent

Usage Instructions
------------------

    Taipei-Torrent mydownload.torrent
    Taipei-Torrent --useDHT "magnet:?xt=urn:btih:bbb6db69965af769f664b6636e7914f8735141b3"

or

    Taipei-Torrent -help

Third-party Packages
--------------------

http://code.google.com/p/bencode-go - Bencode encoder/decoder

http://code.google.com/p/go-nat-pmp - NAT-PMP firewall client

https://github.com/hailiang/gosocks - SOCKS5 proxy support

https://github.com/nictuku/dht      - Distributed Hash Table

https://github.com/nictuku/nettools - Network utilities

Related Projects
----------------

https://github.com/nictuku/Taipei-Torrent is an active fork.

