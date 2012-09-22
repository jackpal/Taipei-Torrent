Taipei Torrent
==============

This is a simple command-line-interface BitTorrent client coded in the go
programming language.

FAQ:
----

Q: Why call it Taipei Torrent?

A: jackpal started writing it while visiting Taipei, Taiwan

Q: What is the license?

A: See the LICENSE file.

Current Status
--------------

Tested on Windows, Linux and Mac OSX.

Development Roadmap
-------------------

+  Support magnet links
+  Implement choke/unchoke logic
+  Full UPnP support (need to be able to search for an unused listener port,
   detect we have already acquired the port,
   release the listener port when we quit.)
+  Clean up source code
+  Deal with TODOs
+  Add a way of quitting other than typing control-C

Download, Install, and Build Instructions
-----------------------------------------

1. Download and install the Go One environment from http://golang.org

2. Use the "go" command to download, install, and build the Taipei-Torrent
app:

    go get github.com/jackpal/Taipei-Torrent

Usage Instructions
------------------

    Taipei-Torrent mydownload.torrent

or

    Taipei-Torrent -help

Third-party Packages
--------------------

http://code.google.com/p/bencode-go - Bencode encoder/decoder

https://github.com/hailiang/gosocks - SOCKS5 proxy support

https://github.com/nictuku/dht      - Distributed Hash Table

https://github.com/nictuku/nettools - Network utilities

Related Projects
----------------

https://github.com/nictuku/Taipei-Torrent is an active fork.

