// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Represents bencode data structure using native Go types: booleans, floats,
// strings, slices, and maps.

package bencode

import (
	"io"
)

// Decode a bencode stream

// Decode parses the stream r and returns the
// generic bencode object representation.  The object representation is a tree
// of Go data types.  The data return value may be one of string,
// int64, uint64, []interface{} or map[string]interface{}.  The slice and map
// elements may in turn contain any of the types listed above and so on.
//
// If Decode encounters a syntax error, it returns with err set to an
// instance of ParseError.  See ParseError documentation for details.
func Decode(r io.Reader) (data interface{}, err error) {
	jb := newDecoder(nil, nil)
	err = Parse(r, jb)
	if err == nil {
		data = jb.Copy()
	}
	return
}

type decoder struct {
	// A value being constructed.
	value interface{}
	// Container entity to flush into.  Can be either []interface{} or
	// map[string]interface{}.
	container interface{}
	// The index into the container interface.  Either int or string.
	index interface{}
}

func newDecoder(container interface{}, key interface{}) *decoder {
	return &decoder{container: container, index: key}
}

func (j *decoder) Int64(i int64) { j.value = int64(i) }

func (j *decoder) Uint64(i uint64) { j.value = uint64(i) }

func (j *decoder) Float64(f float64) { j.value = float64(f) }

func (j *decoder) String(s string) { j.value = s }

func (j *decoder) Bool(b bool) { j.value = b }

func (j *decoder) Null() { j.value = nil }

func (j *decoder) Array() { j.value = make([]interface{}, 0, 8) }

func (j *decoder) Map() { j.value = make(map[string]interface{}) }

func (j *decoder) Elem(i int) Builder {
	v, ok := j.value.([]interface{})
	if !ok {
		v = make([]interface{}, 0, 8)
		j.value = v
	}
/* XXX There is a bug in here somewhere, but append() works fine.
	lens := len(v)
	if cap(v) <= lens {
		news := make([]interface{}, 0, lens*2)
		copy(news, j.value.([]interface{}))
		v = news
	}
	v = v[0 : lens+1]
*/	
	v = append(v, nil)
	j.value = v
	return newDecoder(v, i)
}

func (j *decoder) Key(s string) Builder {
	m, ok := j.value.(map[string]interface{})
	if !ok {
		m = make(map[string]interface{})
		j.value = m
	}
	return newDecoder(m, s)
}

func (j *decoder) Flush() {
	switch c := j.container.(type) {
	case []interface{}:
		index := j.index.(int)
		c[index] = j.Copy()
	case map[string]interface{}:
		index := j.index.(string)
		c[index] = j.Copy()
	}
}

// Get the value built by this builder.
func (j *decoder) Copy() interface{} {
	return j.value
}
