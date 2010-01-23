// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// bencode parser.
// See the bittorrent protocol

package bencode

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strconv"
)

type Reader interface {
	io.Reader
	ReadByte() (c byte, err os.Error)
	UnreadByte() os.Error
}

// Parser
//
// Implements parsing but not the actions.  Those are
// carried out by the implementation of the Builder interface.
// A Builder represents the object being created.
// Calling a method like Int64(i) sets that object to i.
// Calling a method like Elem(i) or Key(s) creates a
// new builder for a subpiece of the object (logically,
// an array element or a map key).
//
// There are two Builders, in other files.
// The decoder builds a generic bencode structures
// in which maps are maps.
// The structBuilder copies data into a possibly
// nested data structure, using the "map keys"
// as struct field names.


// A Builder is an interface implemented by clients and passed
// to the bencode parser.  It gives clients full control over the
// eventual representation returned by the parser.
type Builder interface {
	// Set value
	Int64(i int64)
	Uint64(i uint64)
	String(s string)
	Array()
	Map()

	// Create sub-Builders
	Elem(i int) Builder
	Key(s string) Builder

	// Flush changes to parent Builder if necessary.
	Flush()
}

func collectInt(r Reader, delim byte) (buf []byte, err os.Error) {
	for {
		c, err := r.ReadByte()
		if err != nil {
			return
		}
		if c == delim {
			return
		}
		if !(c == '-' || (c >= '0' && c <= '9')) {
			err = os.NewError("expected digit")
			return
		}
		buf = bytes.AddByte(buf, c)
	}
	return
}

func decodeInt64(r Reader, delim byte) (data int64, err os.Error) {
	buf, err := collectInt(r, delim)
	if err != nil {
		return
	}
	data, err = strconv.Atoi64(string(buf))
	return
}

func decodeString(r Reader) (data string, err os.Error) {
	len, err := decodeInt64(r, ':')
	if err != nil {
		return
	}
	if len < 0 {
		err = os.NewError("Bad string length")
		return
	}
	var buf = make([]byte, len)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return
	}
	data = string(buf)
	return
}

func parse(r Reader, build Builder) (err os.Error) {
Switch:
	c, err := r.ReadByte()
	if err != nil {
		goto exit
	}
	switch {
	case c >= '1' && c <= '9':
		// String
		err = r.UnreadByte()
		if err != nil {
			goto exit
		}
		var str string
		str, err = decodeString(r)
		if err != nil {
			goto exit
		}
		build.String(str)

	case c == 'd':
		// dictionary

		build.Map()
		for {
			c, err = r.ReadByte()
			if err != nil {
				goto exit
			}
			if c == 'e' {
				break
			}
			err = r.UnreadByte()
			if err != nil {
				goto exit
			}
			var key string
			key, err = decodeString(r)
			if err != nil {
				goto exit
			}
			// TODO: in pendantic mode, check for keys in ascending order.
			err = parse(r, build.Key(key))
			if err != nil {
				goto exit
			}
		}

	case c == 'i':
		var buf []byte
		buf, err = collectInt(r, 'e')
		if err != nil {
			goto exit
		}
		var str string
		var i int64
		var i2 uint64
		str = string(buf)
		// If the number is exactly an integer, use that.
		if i, err = strconv.Atoi64(str); err == nil {
			build.Int64(i)
		} else if i2, err = strconv.Atoui64(str); err == nil {
			build.Uint64(i2)
		} else {
			err = os.NewError("Bad integer")
		}

	case c == 'l':
		// array
		build.Array()
		n := 0
		for {
			c, err = r.ReadByte()
			if err != nil {
				goto exit
			}
			if c == 'e' {
				break
			}
			err = r.UnreadByte()
			if err != nil {
				goto exit
			}
			err = parse(r, build.Elem(n))
			if err != nil {
				goto exit
			}
			n++
		}
	default:
		err = os.NewError("Unexpected character")
	}
exit:
	build.Flush()
	return
}

// Parse parses the bencode stream and makes calls to
// the builder to construct a parsed representation.
func Parse(r io.Reader, builder Builder) (err os.Error) {
	rr, ok := r.(Reader)
	if !ok {
		rr = bufio.NewReader(r)
	}
	return parse(rr, builder)
}
