#!/bin/bash
# Test the tracker
set -e
echo "Cleaning.."
go clean
echo "Building..."
# Use "install" so Taipei-Torrent gets placed on the command path, so that it
# can be run as part of the test. Not very hermetic.
go install -race ./...
echo "Running unit tests..."
cd torrent
go test -race
cd ..
cd tracker
go test -race
cd ..
echo "Done"