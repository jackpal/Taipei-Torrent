package bencode

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"testing"
)

type any interface{}

func checkMarshal(expected string, data any) (err os.Error) {
	var b bytes.Buffer
	if err = Marshal(&b, data); err != nil {
		return
	}
	s := b.String()
	if expected != s {
		err = os.NewError(fmt.Sprintf("Expected %s got %s", expected, s))
		return
	}
	return
}

func check(expected string, data any) (err os.Error) {
	if err = checkMarshal(expected, data); err != nil {
		return
	}
	b2 := bytes.NewBufferString(expected)
	val, err := Decode(b2)
	if err != nil {
		err = os.NewError(fmt.Sprint("Failed decoding ", expected, " ", err))
		return
	}
	if err = checkFuzzyEqual(data, val); err != nil {
		return
	}
	return
}

func checkFuzzyEqual(a any, b any) (err os.Error) {
	if !fuzzyEqual(a, b) {
		err = os.NewError(fmt.Sprint(a, " != ", b,
			":", reflect.NewValue(a), "!=", reflect.NewValue(b)))
	}
	return
}

func fuzzyEqual(a, b any) bool {
	return fuzzyEqualValue(reflect.NewValue(a), reflect.NewValue(b))
}

func checkFuzzyEqualValue(a, b reflect.Value) (err os.Error) {
	if !fuzzyEqualValue(a, b) {
		err = os.NewError(fmt.Sprint(a, " != ", b,
			":", a.Interface(), "!=", b.Interface()))
	}
	return
}

func fuzzyEqualInt64(a int64, b reflect.Value) bool {
	switch vb := b.(type) {
	case *reflect.IntValue:
		return a == (vb.Get())
	default:
		return false
	}
	return false
}

func fuzzyEqualArrayOrSlice(va reflect.ArrayOrSliceValue, b reflect.Value) bool {
	switch vb := b.(type) {
	case *reflect.ArrayValue:
		return fuzzyEqualArrayOrSlice2(va, vb)
	case *reflect.SliceValue:
		return fuzzyEqualArrayOrSlice2(va, vb)
	default:
		return false
	}
	return false
}

func deInterface(a reflect.Value) reflect.Value {
	switch va := a.(type) {
	case *reflect.InterfaceValue:
		return va.Elem()
	}
	return a
}

func fuzzyEqualArrayOrSlice2(a reflect.ArrayOrSliceValue, b reflect.ArrayOrSliceValue) bool {
	if a.Len() != b.Len() {
		return false
	}

	for i := 0; i < a.Len(); i++ {
		ea := deInterface(a.Elem(i))
		eb := deInterface(b.Elem(i))
		if !fuzzyEqualValue(ea, eb) {
			return false
		}
	}
	return true
}

func fuzzyEqualMap(a *reflect.MapValue, b *reflect.MapValue) bool {
	key := a.Type().(*reflect.MapType).Key()
	if _, ok := key.(*reflect.StringType); !ok {
		return false
	}
	key = b.Type().(*reflect.MapType).Key()
	if _, ok := key.(*reflect.StringType); !ok {
		return false
	}

	aKeys, bKeys := a.Keys(), b.Keys()

	if len(aKeys) != len(bKeys) {
		return false
	}

	for _, k := range aKeys {
		if !fuzzyEqualValue(a.Elem(k), b.Elem(k)) {
			return false
		}
	}
	return true
}

func fuzzyEqualStruct(a *reflect.StructValue, b *reflect.StructValue) bool {
	numA, numB := a.NumField(), b.NumField()
	if numA != numB {
		return false
	}

	for i := 0; i < numA; i++ {
		if !fuzzyEqualValue(a.Field(i), b.Field(i)) {
			return false
		}
	}
	return true
}

func fuzzyEqualValue(a, b reflect.Value) bool {
	switch va := a.(type) {
	case *reflect.StringValue:
		switch vb := b.(type) {
		case *reflect.StringValue:
			return va.Get() == vb.Get()
		default:
			return false
		}
	case *reflect.IntValue:
		return fuzzyEqualInt64(va.Get(), b)
	case *reflect.ArrayValue:
		return fuzzyEqualArrayOrSlice(va, b)
	case *reflect.SliceValue:
		return fuzzyEqualArrayOrSlice(va, b)
	case *reflect.MapValue:
		switch vb := b.(type) {
		case *reflect.MapValue:
			return fuzzyEqualMap(va, vb)
		default:
			return false
		}
	case *reflect.StructValue:
		switch vb := b.(type) {
		case *reflect.StructValue:
			return fuzzyEqualStruct(va, vb)
		default:
			return false
		}
	case *reflect.InterfaceValue:
		switch vb := b.(type) {
		case *reflect.InterfaceValue:
			return fuzzyEqualValue(va.Elem(), vb.Elem())
		default:
			return false
		}
	default:
		return false
	}
	return false
}

func checkUnmarshal(expected string, data any) (err os.Error) {
	if err = checkMarshal(expected, data); err != nil {
		return
	}
	dataValue := reflect.NewValue(data)
	newOne := reflect.MakeZero(dataValue.Type())
	buf := bytes.NewBufferString(expected)
	if err = UnmarshalValue(buf, newOne); err != nil {
		return
	}
	if err = checkFuzzyEqualValue(dataValue, newOne); err != nil {
		return
	}
	return
}

type SVPair struct {
	s string
	v any
}

func TestDecode(t *testing.T) {
	tests := []SVPair{
		SVPair{"i0e", int64(0)},
		SVPair{"i0e", 0},
		SVPair{"i100e", 100},
		SVPair{"i-100e", -100},
		SVPair{"1:a", "a"},
		SVPair{"2:a\"", "a\""},
		SVPair{"11:0123456789a", "0123456789a"},
		SVPair{"le", []int64{}},
		SVPair{"li1ei2ee", []int{1, 2}},
		SVPair{"l3:abc3:defe", []string{"abc", "def"}},
		SVPair{"li42e3:abce", []any{42, "abc"}},
		SVPair{"de", map[string]any{}},
		SVPair{"d3:cati1e3:dogi2ee", map[string]any{"cat": 1, "dog": 2}},
	}
	for _, sv := range tests {
		if err := check(sv.s, sv.v); err != nil {
			t.Error(err.String())
		}
	}
}

type structA struct {
	A int    "a"
	B string "b"
}

func TestUnmarshal(t *testing.T) {
	type structNested struct {
		T string            "t"
		Y string            "y"
		Q string            "q"
		A map[string]string "a"
	}
	innerDict := map[string]string{"id": "abcdefghij0123456789"}
	nestedDictionary := structNested{"aa", "q", "ping", innerDict}

	tests := []SVPair{
		SVPair{"i0e", int64(0)},
		SVPair{"i0e", 0},
		SVPair{"i100e", 100},
		SVPair{"i-100e", -100},
		SVPair{"1:a", "a"},
		SVPair{"2:a\"", "a\""},
		SVPair{"11:0123456789a", "0123456789a"},
		SVPair{"le", []int64{}},
		SVPair{"li1ei2ee", []int{1, 2}},
		SVPair{"l3:abc3:defe", []string{"abc", "def"}},
		SVPair{"li42e3:abce", []any{42, "abc"}},
		SVPair{"de", map[string]any{}},
		SVPair{"d3:cati1e3:dogi2ee", map[string]any{"cat": 1, "dog": 2}},
		SVPair{"d1:ai10e1:b3:fooe", structA{10, "foo"}},
		SVPair{"d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe", nestedDictionary},
	}
	for _, sv := range tests {
		if err := checkUnmarshal(sv.s, sv.v); err != nil {
			t.Error(err.String())
		}
	}
}
