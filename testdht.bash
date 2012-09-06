#!/bin/bash
# Test the torrent

./Taipei-Torrent -fileDir=testData/downloads -port 63881 -useUPnP=false -useDHT -trackerLessMode=true testData/a.torrent
