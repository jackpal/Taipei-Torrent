// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Marshalling and unmarshalling of
// bit torrent bencode data into Go structs using reflection.
//
// Based upon the standard Go language JSON package.

package bencode

import (
	"errors"
	"fmt"
	"io"

	"reflect"
	"sort"
	"strings"
)

type structBuilder struct {
	val reflect.Value

	// if map_ != nil, write val to map_[key] on each change
	map_ reflect.Value
	key  reflect.Value
}

var nobuilder *structBuilder

func isfloat(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func setfloat(v reflect.Value, f float64) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(f)
	}
}

func setint(val reflect.Value, i int64) {
	switch v := val; v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(i))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v.SetUint(uint64(i))
	case reflect.Interface:
		v.Set(reflect.ValueOf(i))
	default:
		panic("setint called for bogus type: " + val.Kind().String())
	}
}

// If updating b.val is not enough to update the original,
// copy a changed b.val out to the original.
func (b *structBuilder) Flush() {
	if b == nil {
		return
	}
	if b.map_.IsValid() {
		b.map_.SetMapIndex(b.key, b.val)
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

	switch b.val.Kind() {
	case reflect.String:
		if !b.val.CanSet() {
			x := ""
			b.val = reflect.ValueOf(&x).Elem()

		}
		b.val.SetString(s)
	case reflect.Interface:
		b.val.Set(reflect.ValueOf(s))
	}
}

func (b *structBuilder) Array() {
	if b == nil {
		return
	}
	if v := b.val; v.Kind() == reflect.Slice {
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 8))
		}
	}
}

func (b *structBuilder) Elem(i int) Builder {
	if b == nil || i < 0 {
		return nobuilder
	}
	switch v := b.val; v.Kind() {
	case reflect.Array:
		if i < v.Len() {
			return &structBuilder{val: v.Index(i)}
		}
	case reflect.Slice:
		if i >= v.Cap() {
			n := v.Cap()
			if n < 8 {
				n = 8
			}
			for n <= i {
				n *= 2
			}
			nv := reflect.MakeSlice(v.Type(), v.Len(), n)
			reflect.Copy(nv, v)
			v.Set(nv)
		}
		if v.Len() <= i && i < v.Cap() {
			v.SetLen(i + 1)
		}
		if i < v.Len() {
			return &structBuilder{val: v.Index(i)}
		}
	}
	return nobuilder
}

func (b *structBuilder) Map() {
	if b == nil {
		return
	}
	if v := b.val; v.Kind() == reflect.Ptr && v.IsNil() {
		if v.IsNil() {
			v.Set(reflect.Zero(v.Type().Elem()).Addr())
			b.Flush()
		}
		b.map_ = reflect.Value{}
		b.val = v.Elem()
	}
	if v := b.val; v.Kind() == reflect.Map && v.IsNil() {
		v.Set(reflect.MakeMap(v.Type()))
	}
}

func (b *structBuilder) Key(k string) Builder {
	if b == nil {
		return nobuilder
	}
	switch v := reflect.Indirect(b.val); v.Kind() {
	case reflect.Struct:
		t := v.Type()
		// Case-insensitive field lookup.
		k = strings.ToLower(k)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if strings.ToLower(string(field.Tag)) == k ||
				strings.ToLower(field.Name) == k {
				return &structBuilder{val: v.Field(i)}
			}
		}
	case reflect.Map:
		t := v.Type()
		if t.Key() != reflect.TypeOf(k) {
			break
		}
		key := reflect.ValueOf(k)
		elem := v.MapIndex(key)
		if !elem.IsValid() {
			v.SetMapIndex(key, reflect.Zero(t.Elem()))
			elem = v.MapIndex(key)
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

func Unmarshal(r io.Reader, val interface{}) (err error) {
	// If e represents a value, the answer won't get back to the
	// caller.  Make sure it's a pointer.
	if reflect.TypeOf(val).Kind() != reflect.Ptr {
		err = errors.New("Attempt to unmarshal into a non-pointer")
		return
	}
	err = UnmarshalValue(r, reflect.Indirect(reflect.ValueOf(val)))
	return
}

// This API is public primarily to make testing easier, but it is available if you
// have a use for it.

func UnmarshalValue(r io.Reader, v reflect.Value) (err error) {
	var b *structBuilder

	// XXX: Decide if the extra codnitions are needed. Affect map?
	if ptr := v; ptr.Kind() == reflect.Ptr {
		if slice := ptr.Elem(); slice.Kind() == reflect.Slice || slice.Kind() == reflect.Int || slice.Kind() == reflect.String {
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

func (e *MarshalError) Error() string {
	return "bencode cannot encode value of type " + e.T.String()
}

func writeArrayOrSlice(w io.Writer, val reflect.Value) (err error) {
	_, err = fmt.Fprint(w, "l")
	if err != nil {
		return
	}
	for i := 0; i < val.Len(); i++ {
		if err := writeValue(w, val.Index(i)); err != nil {
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

func writeSVList(w io.Writer, svList StringValueArray) (err error) {
	sort.Sort(svList)

	for _, sv := range svList {
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

func writeMap(w io.Writer, val reflect.Value) (err error) {
	key := val.Type().Key()
	if key.Kind() != reflect.String {
		return &MarshalError{val.Type()}
	}
	_, err = fmt.Fprint(w, "d")
	if err != nil {
		return
	}

	keys := val.MapKeys()

	// Sort keys

	svList := make(StringValueArray, len(keys))
	for i, key := range keys {
		svList[i].key = key.String()
		svList[i].value = val.MapIndex(key)
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

func writeStruct(w io.Writer, val reflect.Value) (err error) {
	_, err = fmt.Fprint(w, "d")
	if err != nil {
		return
	}

	typ := val.Type()

	numFields := val.NumField()
	svList := make(StringValueArray, numFields)

	for i := 0; i < numFields; i++ {
		field := typ.Field(i)
		key := field.Name
		if len(field.Tag) > 0 {
			key = string(field.Tag)
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

func writeValue(w io.Writer, val reflect.Value) (err error) {
	if !val.IsValid() {
		err = errors.New("Can't write null value")
		return
	}

	switch v := val; v.Kind() {
	case reflect.String:
		s := v.String()
		_, err = fmt.Fprintf(w, "%d:%s", len(s), s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		_, err = fmt.Fprintf(w, "i%de", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		_, err = fmt.Fprintf(w, "i%de", v.Uint())
	case reflect.Array:
		err = writeArrayOrSlice(w, v)
	case reflect.Slice:
		err = writeArrayOrSlice(w, v)
	case reflect.Map:
		err = writeMap(w, v)
	case reflect.Struct:
		err = writeStruct(w, v)
	case reflect.Interface:
		err = writeValue(w, v.Elem())
	default:
		err = &MarshalError{val.Type()}
	}
	return
}

func isValueNil(val reflect.Value) bool {
	if !val.IsValid() {
		return true
	}
	switch v := val; v.Kind() {
	case reflect.Interface:
		return isValueNil(v.Elem())
	default:
		return false
	}
	return false
}

func Marshal(w io.Writer, val interface{}) error {
	return writeValue(w, reflect.ValueOf(val))
}
