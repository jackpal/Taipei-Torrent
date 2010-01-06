#!/bin/bash
#
# Pass 1: clean everything

bash clean.bash

# Pass 2: make everything

cd bencode
make
make install
cd ..
make
