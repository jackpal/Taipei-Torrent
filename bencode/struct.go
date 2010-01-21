// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Marshalling and unmarshalling of
// bit torrent bencode data into Go structs using reflection.
//
// Based upon the standard Go language JSON package.

package bencode

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
)

type structBuilder struct {
	val reflect.Value

	// if map_ != nil, write val to map_[key] on each change
	map_ *reflect.MapValue
	key  reflect.Value
}

var nobuilder *structBuilder

func isfloat(v reflect.Value) bool {
	switch v.(type) {
	case *reflect.FloatValue, *reflect.Float32Value, *reflect.Float64Value:
		return true
	}
	return false
}

func setfloat(v reflect.Value, f float64) {
	switch v := v.(type) {
	case *reflect.FloatValue:
		v.Set(float(f))
	case *reflect.Float32Value:
		v.Set(float32(f))
	case *reflect.Float64Value:
		v.Set(float64(f))
	}
}

func setint(val reflect.Value, i int64) {
	switch v := val.(type) {
	case *reflect.IntValue:
		v.Set(int(i))
	case *reflect.Int8Value:
		v.Set(int8(i))
	case *reflect.Int16Value:
		v.Set(int16(i))
	case *reflect.Int32Value:
		v.Set(int32(i))
	case *reflect.Int64Value:
		v.Set(int64(i))
	case *reflect.UintValue:
		v.Set(uint(i))
	case *reflect.Uint8Value:
		v.Set(uint8(i))
	case *reflect.Uint16Value:
		v.Set(uint16(i))
	case *reflect.Uint32Value:
		v.Set(uint32(i))
	case *reflect.Uint64Value:
		v.Set(uint64(i))
	case *reflect.InterfaceValue:
		v.Set(reflect.NewValue(i))
	}
}

// If updating b.val is not enough to update the original,
// copy a changed b.val out to the original.
func (b *structBuilder) Flush() {
	if b == nil {
		return
	}
	if b.map_ != nil {
		b.map_.SetElem(b.key, b.val)
	}
}

func (b *structBuilder) Int64(i int64) {
	if b == nil {
		return
	}
	v := b.val
	if isfloat(v) {
		setfloat(v, float64(i))
	} else {
		setint(v, i)
	}
}

func (b *structBuilder) Uint64(i uint64) {
	if b == nil {
		return
	}
	v := b.val
	if isfloat(v) {
		setfloat(v, float64(i))
	} else {
		setint(v, int64(i))
	}
}

func (b *structBuilder) Float64(f float64) {
	if b == nil {
		return
	}
	v := b.val
	if isfloat(v) {
		setfloat(v, f)
	} else {
		setint(v, int64(f))
	}
}

func (b *structBuilder) String(s string) {
	if b == nil {
		return
	}

	switch v := b.val.(type) {
	case *reflect.StringValue:
		v.Set(s)
	case *reflect.InterfaceValue:
		v.Set(reflect.NewValue(s))
	}
}

func (b *structBuilder) Array() {
	if b == nil {
		return
	}
	if v, ok := b.val.(*reflect.SliceValue); ok {
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type().(*reflect.SliceType), 0, 8))
		}
	}
}

func (b *structBuilder) Elem(i int) Builder {
	if b == nil || i < 0 {
		return nobuilder
	}
	switch v := b.val.(type) {
	case *reflect.ArrayValue:
		if i < v.Len() {
			return &structBuilder{val: v.Elem(i)}
		}
	case *reflect.SliceValue:
		if i >= v.Cap() {
			n := v.Cap()
			if n < 8 {
				n = 8
			}
			for n <= i {
				n *= 2
			}
			nv := reflect.MakeSlice(v.Type().(*reflect.SliceType), v.Len(), n)
			reflect.ArrayCopy(nv, v)
			v.Set(nv)
		}
		if v.Len() <= i && i < v.Cap() {
			v.SetLen(i + 1)
		}
		if i < v.Len() {
			return &structBuilder{val: v.Elem(i)}
		}
	}
	return nobuilder
}

func (b *structBuilder) Map() {
	if b == nil {
		return
	}
	if v, ok := b.val.(*reflect.PtrValue); ok && v.IsNil() {
		if v.IsNil() {
			v.PointTo(reflect.MakeZero(v.Type().(*reflect.PtrType).Elem()))
			b.Flush()
		}
		b.map_ = nil
		b.val = v.Elem()
	}
	if v, ok := b.val.(*reflect.MapValue); ok && v.IsNil() {
		v.Set(reflect.MakeMap(v.Type().(*reflect.MapType)))
	}
}

func (b *structBuilder) Key(k string) Builder {
	if b == nil {
		return nobuilder
	}
	switch v := reflect.Indirect(b.val).(type) {
	case *reflect.StructValue:
		t := v.Type().(*reflect.StructType)
		// Case-insensitive field lookup.
		k = strings.ToLower(k)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if strings.ToLower(field.Tag) == k ||
				strings.ToLower(field.Name) == k {
				return &structBuilder{val: v.Field(i)}
			}
		}
	case *reflect.MapValue:
		t := v.Type().(*reflect.MapType)
		if t.Key() != reflect.Typeof(k) {
			break
		}
		key := reflect.NewValue(k)
		elem := v.Elem(key)
		if elem == nil {
			v.SetElem(key, reflect.MakeZero(t.Elem()))
			elem = v.Elem(key)
		}
		return &structBuilder{val: elem, map_: v, key: key}
	}
	return nobuilder
}

// Unmarshal parses the bencode syntax string s and fills in
// an arbitrary struct or slice pointed at by val.
// It uses the reflect package to assign to fields
// and arrays embedded in val.  Well-formed data that does not fit
// into the struct is discarded.
//
// For example, given these definitions:
//
//	type Email struct {
//		Where string;
//		Addr string;
//	}
//
//	type Result struct {
//		Name string;
//		Phone string;
//		Email []Email
//	}
//
//	var r = Result{ "name", "phone", nil }
//
// unmarshalling the bencode syntax string
//
//	d5:emailld5:where4:home4:addr15:gre@example.come\
//  d5:where4:work4:addr12:gre@work.comee4:name14:Gr\
//  ace R. Emlin7:address15:123 Main Streete
//
// via Unmarshal(s, &r) is equivalent to assigning
//
//	r = Result{
//		"Grace R. Emlin",	// name
//		"phone",		// no phone given
//		[]Email{
//			Email{ "home", "gre@example.com" },
//			Email{ "work", "gre@work.com" }
//		}
//	}
//
// Note that the field r.Phone has not been modified and
// that the bencode field "address" was discarded.
//
// Because Unmarshal uses the reflect package, it can only
// assign to upper case fields.  Unmarshal uses a case-insensitive
// comparison to match bencode field names to struct field names.
//
// If you provide a tag string for a struct member, the tag string
// will be used as the bencode dictionary key for that member.
//
// To unmarshal a top-level bencode array, pass in a pointer to an empty
// slice of the correct type.
//

func Unmarshal(r io.Reader, val interface{}) (err os.Error) {
	// If e represents a value, the answer won't get back to the
	// caller.  Make sure it's a pointer.
	if _, ok := reflect.Typeof(val).(*reflect.PtrType); !ok {
		err = os.ErrorString("Attempt to unmarshal into a non-pointer")
		return
	}
	err = UnmarshalValue(r, reflect.NewValue(val))
	return
}

// This API is public primarily to make testing easier, but it is available if you
// have a use for it.

func UnmarshalValue(r io.Reader, v reflect.Value) (err os.Error) {
	var b *structBuilder

	// If val is a pointer to a slice, we append to the slice.
	if ptr, ok := v.(*reflect.PtrValue); ok {
		if slice, ok := ptr.Elem().(*reflect.SliceValue); ok {
			b = &structBuilder{val: slice}
		}
	}

	if b == nil {
		b = &structBuilder{val: v}
	}

	err = Parse(r, b)
	return
}

type MarshalError struct {
	T reflect.Type
}

func (e *MarshalError) String() string {
	return "bencode cannot encode value of type " + e.T.String()
}

func writeArrayOrSlice(w io.Writer, val reflect.ArrayOrSliceValue) (err os.Error) {
	_, err = fmt.Fprint(w, "l")
	if err != nil {
		return
	}
	for i := 0; i < val.Len(); i++ {
		if err := writeValue(w, val.Elem(i)); err != nil {
			return err
		}
	}

	_, err = fmt.Fprint(w, "e")
	if err != nil {
		return
	}
	return nil
}

type StringValue struct {
	key   string
	value reflect.Value
}

type StringValueArray []StringValue

// Satisfy sort.Interface

func (a StringValueArray) Len() int { return len(a) }

func (a StringValueArray) Less(i, j int) bool { return a[i].key < a[j].key }

func (a StringValueArray) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

func writeSVList(w io.Writer, svList StringValueArray) (err os.Error) {
	sort.Sort(svList)

	for _, sv := range (svList) {
		if isValueNil(sv.value) {
			continue // Skip null values
		}
		s := sv.key
		_, err = fmt.Fprintf(w, "%d:%s", len(s), s)
		if err != nil {
			return
		}

		if err = writeValue(w, sv.value); err != nil {
			return
		}
	}
	return
}


func writeMap(w io.Writer, val *reflect.MapValue) (err os.Error) {
	key := val.Type().(*reflect.MapType).Key()
	if _, ok := key.(*reflect.StringType); !ok {
		return &MarshalError{val.Type()}
	}
	_, err = fmt.Fprint(w, "d")
	if err != nil {
		return
	}

	keys := val.Keys()

	// Sort keys

	svList := make(StringValueArray, len(keys))
	for i, key := range (keys) {
		svList[i].key = key.(*reflect.StringValue).Get()
		svList[i].value = val.Elem(key)
	}

	err = writeSVList(w, svList)
	if err != nil {
		return
	}

	_, err = fmt.Fprint(w, "e")
	if err != nil {
		return
	}
	return
}

func writeStruct(w io.Writer, val *reflect.StructValue) (err os.Error) {
	_, err = fmt.Fprint(w, "d")
	if err != nil {
		return
	}

	typ := val.Type().(*reflect.StructType)

	numFields := val.NumField()
	svList := make(StringValueArray, numFields)

	for i := 0; i < numFields; i++ {
		field := typ.Field(i)
		key := field.Name
		if len(field.Tag) > 0 {
			key = field.Tag
		}
		svList[i].key = key
		svList[i].value = val.Field(i)
	}

	err = writeSVList(w, svList)
	if err != nil {
		return
	}

	_, err = fmt.Fprint(w, "e")
	if err != nil {
		return
	}
	return
}

func writeValue(w io.Writer, val reflect.Value) (err os.Error) {
	if val == nil {
		err = os.NewError("Can't write null value")
		return
	}

	switch v := val.(type) {
	case *reflect.StringValue:
		s := v.Get()
		_, err = fmt.Fprintf(w, "%d:%s", len(s), s)
	case *reflect.IntValue:
		_, err = fmt.Fprintf(w, "i%de", v.Get())
	case *reflect.UintValue:
		_, err = fmt.Fprintf(w, "i%de", v.Get())
	case *reflect.Int64Value:
		_, err = fmt.Fprintf(w, "i%de", v.Get())
	case *reflect.Uint64Value:
		_, err = fmt.Fprintf(w, "i%de", v.Get())
	case *reflect.ArrayValue:
		err = writeArrayOrSlice(w, v)
	case *reflect.SliceValue:
		err = writeArrayOrSlice(w, v)
	case *reflect.MapValue:
		err = writeMap(w, v)
	case *reflect.StructValue:
		err = writeStruct(w, v)
	case *reflect.InterfaceValue:
		err = writeValue(w, v.Elem())
	default:
		err = &MarshalError{val.Type()}
	}
	return
}

func isValueNil(val reflect.Value) bool {
	if val == nil {
		return true
	}
	switch v := val.(type) {
	case *reflect.InterfaceValue:
		return isValueNil(v.Elem())
	default:
		return false
	}
	return false
}

func Marshal(w io.Writer, val interface{}) os.Error {
	return writeValue(w, reflect.NewValue(val))
}
