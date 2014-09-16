Taipei Torrent
==============

This is a simple command-line-interface BitTorrent client coded in the go
programming language.

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

FAQ:
----

Q: Why is it named Taipei Torrent?

A: I started writing it while visiting beautiful Taipei, Taiwan

Q: What is the license?

A: See the LICENSE file.

Current Status
--------------

+ Tested on Go 1.3
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

    go get github.com/remerge/torrent

Usage Instructions
------------------

    Taipei-Torrent mydownload.torrent
    Taipei-Torrent --useDHT "magnet:?xt=urn:btih:bbb6db69965af769f664b6636e7914f8735141b3"

or

    Taipei-Torrent -help

Third-party Packages
--------------------

http://code.google.com/p/bencode-go - Bencode encoder/decoder

Google+ Community
-----------------

https://plus.google.com/u/0/communities/100997865549971977580

