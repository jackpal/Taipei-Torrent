// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Represents bencode data structure using native Go types: booleans, floats,
// strings, arrays, and maps.

package bencode

import (
	"container/vector"
	"io"
	"os"
)

// Decode a bencode stream

// Decode parses the stream r and returns the
// generic bencode object representation.  The object representation is a tree
// of Go data types.  The data return value may be one of string,
// int64, uint64, []interface{} or map[string]interface{}.  The array and map
// elements may in turn contain any of the types listed above and so on.
//
// If Decode encounters a syntax error, it returns with err set to an
// instance of ParseError.  See ParseError documentation for details.
func Decode(r io.Reader) (data interface{}, err os.Error) {
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
	// Container entity to flush into.  Can be either vector.Vector or
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

func (j *decoder) Array() { j.value = new(vector.Vector) }

func (j *decoder) Map() { j.value = make(map[string]interface{}) }

func (j *decoder) Elem(i int) Builder {
	v, ok := j.value.(*vector.Vector)
	if !ok {
		v = new(vector.Vector)
		j.value = v
	}
	if v.Len() <= i {
		v.Resize(i+1, (i+1)*2)
	}
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
	case *vector.Vector:
		index := j.index.(int)
		c.Set(index, j.Copy())
	case map[string]interface{}:
		index := j.index.(string)
		c[index] = j.Copy()
	}
}

// Get the value built by this builder.
func (j *decoder) Copy() interface{} {
	switch v := j.value.(type) {
	case *vector.Vector:
		return v.Copy()
	}
	return j.value
}
