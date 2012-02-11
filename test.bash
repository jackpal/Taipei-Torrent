#!/bin/bash
# Test the torrent

./Taipei-Torrent -torrent=testData/a.torrent -fileDir=testData/downloads -port 63881 -useUPnP=false -useDHT -trackerLessMode=true
