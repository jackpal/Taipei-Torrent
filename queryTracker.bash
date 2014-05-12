#!/bin/bash
# Test the tracker
set -e

TRACKER=http://127.0.0.1:8080
ANNOUNCE=$TRACKER/announce
SCRAPE=$TRACKER/scrape

curl $SCRAPE

# 3 peers
curl $ANNOUNCE?port=6001\&compact=1\&uploaded=0\&downloaded=0\&left=123\&event=started\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4
curl $ANNOUNCE?port=6002\&compact=1\&uploaded=0\&downloaded=0\&left=123\&event=startedx\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4
curl $ANNOUNCE?port=6003\&compact=1\&uploaded=0\&downloaded=0\&left=123\&event=started\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4

# check in again
curl $ANNOUNCE?port=6001\&compact=1\&uploaded=10\&downloaded=0\&left=123\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4
curl $ANNOUNCE?port=6002\&compact=1\&uploaded=10\&downloaded=0\&left=123\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4
curl $ANNOUNCE?port=6003\&compact=1\&uploaded=10\&downloaded=0\&left=123\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4

# Completed event
curl $ANNOUNCE?port=6001\&compact=1\&uploaded=10\&downloaded=0\&left=0\&event=completed\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4

# Stopped event
curl $ANNOUNCE?port=6001\&compact=1\&uploaded=10\&downloaded=0\&left=0\&event=stopped\&info_hash=%a4%1d%1f%89\(dT%b1%8d%8dL%b2%e0/%fe%11Xtv%c4

curl $SCRAPE
