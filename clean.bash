#!/bin/bash
#
set -e

make clean

DEPS="bencode taipei"
for dep in ${DEPS}; do
	cd $dep ; make clean || true; cd ..
done
