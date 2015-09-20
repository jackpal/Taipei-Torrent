Taipei Torrent
==============

This is a simple command-line-interface BitTorrent client coded in the go
programming language.

[![Build Status](https://travis-ci.org/jackpal/Taipei-Torrent.svg)](https://travis-ci.org/jackpal/Taipei-Torrent)

Features:
---------

+ Supports multiple torrent files
+ Magnet links
+ DHT
+ IPv6
+ UDP trackers
+ UPnP / NAT-PMP automatic firewall configuration
+ Socks5 proxy support

Additional Features:
--------------------

+ It can act as a tracker if you start it with the --createTracker flag
+ SFTP file system proxying enables torrenting large torrents on systems with
  limited local file storage.

FAQ:
----

Q: Why is it named Taipei Torrent?

A: I started writing it while visiting beautiful Taipei, Taiwan

Q: What is the license?

A: See the LICENSE file.

Current Status
--------------

+ Tested on Go 1.4.2 and tip.
+ Tested on Windows, Linux and Mac OS X.
+ People tell me they've run it on Android, too.

Development Roadmap
-------------------

+ Full UPnP support (need to be able to search for an unused listener port,
  detect we have already acquired the port, defend the report against router
  reboots, release the listener port when we quit.)
+ Clean up source code
+ Deal with TODOs
+ Perhaps a web-based status UI.

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

https://github.com/jackpal/bencode-go - Bencode encoder/decoder

http://github.com/jackpal/gateway - LAN gateway discovery

http://github.com/jackpal/go-nat-pmp - NAT-PMP firewall client

https://github.com/nictuku/dht      - Distributed Hash Table

https://github.com/nictuku/nettools - Network utilities

https://github.com/pkg/sftp - SFTP protocol


Google+ Community
-----------------

https://plus.google.com/u/0/communities/100997865549971977580

Other Notable Go BitTorrent Implementations
-------------------------------------------

I haven't used these, but they may be worth checking out:

https://github.com/anacrolix/torrent

