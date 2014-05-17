#!/bin/bash
# Test the tracker
set -e
echo "Cleaning.."
go clean
echo "Building..."
# Go get is used to publish the resulting Taipei-Torrent file, so that it can be used by the tracker_test
# There's probably a better way to do this. (That would allow testing before publishing.)
go get -race
echo "Running unit tests..."
cd torrent
go test -race
cd ..
cd tracker
go test -race
cd ..
echo "Done"