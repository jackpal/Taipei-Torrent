#!/bin/bash
#
set -e

# Pass 1: clean everything

bash clean.bash

# Pass 2: make everything

DEPS="http bencode taipei"
for dep in ${DEPS}; do
	cd $dep ; make ; make install ; cd ..
done
make
