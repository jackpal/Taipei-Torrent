#!/bin/bash
# Test the torrent
set -e
echo "Building..."
go clean
go build -race
echo "Running unit tests..."
cd torrent
go test -race
cd ..
echo "Running a torrent. Type control-C to exit"
# ./Taipei-Torrent -fileDir=testData/downloads -port=63881 -useUPnP=true testData/a.torrent
# ./Taipei-Torrent -fileDir=testData/downloads -port=63881 -useNATPMP -gateway 192.168.1.1 testData/a.torrent
./Taipei-Torrent -fileDir=testData/downloads -port=63881 --seedRatio=0 testData/a.torrent
