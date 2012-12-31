#!/bin/bash
# Test the torrent
set -e

go build
./Taipei-Torrent -fileDir=testData/downloads -port=63881 -useUPnP=true testData/a.torrent
