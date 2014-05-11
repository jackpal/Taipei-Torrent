#!/bin/bash
# Test the tracker
set -e
echo "Building..."
go clean
go build -race
echo "Running unit tests..."
cd torrent
go test -race
cd ..
cd tracker
go test -race
cd ..
echo "Running a tracker. Type control-C to exit"
./Taipei-Torrent  -createTracker -port=63881 testData/a.torrent
