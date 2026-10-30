// Package jsonx holds the JSON helpers that keep the server's output compatible with the legacy Python player:
// Python-style float formatting, Python truthiness for client values, objects that keep their key order, and the
// one-space indentation of json.dumps(indent=1).
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Float is a float64 that encodes like Python's json.dumps: integral values keep a ".0" (40.0, not 40).
type Float float64

// MarshalJSON implements json.Marshaler.
func (f Float) MarshalJSON() ([]byte, error) {
	return []byte(FormatFloat(float64(f))), nil
}

// FormatFloat formats f like Python's repr(float). NaN and infinities, which aren't JSON, become null.
func FormatFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	if a := math.Abs(f); a >= 1e16 || a < 1e-4 {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// Round3 rounds like Python's round(x, 3): correctly rounded on the exact binary value.
func Round3(f float64) float64 {
	r, err := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 3, 64), 64)
	if err != nil {
		return f
	}
	return r
}

// FloatPtr returns a *Float for v, or nil.
func FloatPtr(v *float64) *Float {
	if v == nil {
		return nil
	}
	f := Float(*v)
	return &f
}

// Marshal encodes v without HTML escaping and without a trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Indent1 re-indents a JSON document like Python's json.dumps(obj, indent=1): one space per level, ", " never
// used inside lines, ": " after keys. Only whitespace changes, so numbers and strings stay byte for byte.
func Indent1(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", " "); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Truthy reports whether a JSON value is truthy in Python: null, false, 0, "", [] and {} are not.
func Truthy(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case 'n', 'f':
		return false
	case 't':
		return true
	case '"':
		return len(s) > 2
	case '[', '{':
		inner := bytes.TrimSpace(s[1 : len(s)-1])
		return len(inner) > 0
	}
	f, err := strconv.ParseFloat(string(s), 64)
	return err != nil || f != 0
}

// Kind returns the JSON kind of raw: "object", "array", "string", "number", "bool", "null", or "" if empty.
func Kind(raw json.RawMessage) string {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return ""
	}
	switch s[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	}
	return "number"
}

// Number parses raw as a JSON number.
func Number(raw json.RawMessage) (float64, bool) {
	if Kind(raw) != "number" {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
	return f, err == nil
}

// Object is a JSON object that keeps its keys in order, like a Python dict. Setting an existing key replaces the
// value in place; a new key goes to the end. A duplicate key in the input keeps its first position and last value.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// ErrNotObject is returned by ParseObject when the value isn't a JSON object.
var ErrNotObject = errors.New("not a JSON object")

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{vals: map[string]json.RawMessage{}}
}

// ParseObject parses a JSON object, keeping key order and the raw bytes of every value.
func ParseObject(raw []byte) (*Object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, ErrNotObject
	}
	o := NewObject()
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("object key %v is not a string", kt)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.Set(k, v)
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("extra data after the object")
	}
	return o, nil
}

// Get returns the raw value of key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Set sets key to the raw JSON value v.
func (o *Object) Set(key string, v json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// SetValue marshals v and sets it as key.
func (o *Object) SetValue(key string, v any) error {
	b, err := Marshal(v)
	if err != nil {
		return err
	}
	o.Set(key, b)
	return nil
}

// Keys returns the keys in order.
func (o *Object) Keys() []string {
	return append([]string(nil), o.keys...)
}

// MarshalJSON implements json.Marshaler, keeping key order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
