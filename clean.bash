#!/bin/bash
#
set -e

DEPS="http bencode taipei"
for dep in ${DEPS}; do
	cd $dep ; make clean || true; cd ..
done
