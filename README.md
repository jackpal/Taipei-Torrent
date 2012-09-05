Taipei Torrent
==============

This is a simple command-line-interface BitTorrent client coded in the go
programming language.

FAQ:
----

Q: Why call it Taipei Torrent?

A: jackpal started writing it while visiting Taipei, Taiwan

Q: What is the license?

A: The code in the bencode subdirectory is controlled by the LICENSE file in
that directory.

The code in the rest of Taipei Torrent is controlled by the LICENSE file in the
top level.

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

    go get github.com/nictuku/Taipei-Torrent

Usage Instructions
------------------

    Taipei-Torrent -torrent mydownload.torrent

or

    Taipei-Torrent --help

Build Status
----------------

Currently ![](http://goci.me/project/image/github.com/nictuku/Taipei-Torrent)

[GoCI project status](http://goci.me/project/github.com/nictuku/Taipei-Torrent)

Related Projects
----------------

https://github.com/jackpal/Taipei-Torrent the original project from which this is based.
