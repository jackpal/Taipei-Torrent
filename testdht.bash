#!/bin/bash
exec ./Taipei-Torrent -fileDir=testData/downloads -port 63881 -useUPnP=false -useDHT -trackerlessMode=true testData/a.torrent
