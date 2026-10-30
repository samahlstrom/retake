package jsonx

import (
	"encoding/json"
	"math"
	"testing"
)

func TestFormatFloat(t *testing.T) {
	// Expected values are Python's repr(float).
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{40, "40.0"},
		{30, "30.0"},
		{29.97, "29.97"},
		{math.Nextafter(0.3, 1), "0.30000000000000004"}, // 0.1 + 0.2 at run time
		{0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{-2.5, "-2.5"},
		{1e16, "1e+16"},
		{1.5e-5, "1.5e-05"},
		{0.0001, "0.0001"},
		{123456789012345.6, "123456789012345.6"},
		{math.NaN(), "null"},
		{math.Inf(1), "null"},
	} {
		if got := FormatFloat(tc.in); got != tc.want {
			t.Errorf("FormatFloat(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRound3(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{40.0000001, 40}, {29.97002997, 29.97}, {1.0005, 1}, {2.0004999, 2}, {10.0109999, 10.011},
	} {
		if got := Round3(tc.in); got != tc.want {
			t.Errorf("Round3(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTruthy(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"null", false}, {"false", false}, {"true", true}, {"0", false}, {"0.0", false}, {"-0", false},
		{"0e5", false}, {"1", true}, {"-0.5", true}, {`""`, false}, {`"x"`, true}, {"[]", false},
		{"[ ]", false}, {"[0]", true}, {"{}", false}, {"{ \n }", false}, {`{"a":1}`, true}, {"", false},
	} {
		if got := Truthy(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("Truthy(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestObjectKeepsOrder(t *testing.T) {
	o, err := ParseObject([]byte(`{"z": 1, "video": {"a": 1}, "a": [1, 2], "z": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	o.Set("video", json.RawMessage(`"replaced"`))
	if err := o.SetValue("new", map[string]string{"k": "<&>"}); err != nil {
		t.Fatal(err)
	}
	b, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"z":3,"video":"replaced","a":[1, 2],"new":{"k":"<&>"}}`
	if string(b) != want {
		t.Errorf("got %s\nwant %s", b, want)
	}
}

func TestParseObjectRejects(t *testing.T) {
	for _, in := range []string{`[]`, `1`, `"x"`, `{"a":1} {}`, `{"a":}`, ``} {
		if _, err := ParseObject([]byte(in)); err == nil {
			t.Errorf("ParseObject(%q) succeeded", in)
		}
	}
}

func TestIndent1MatchesPython(t *testing.T) {
	// json.dumps({"a": 1, "b": [1, 2], "c": {}, "d": [], "e": "é"}, indent=1, ensure_ascii=False)
	want := "{\n \"a\": 1,\n \"b\": [\n  1,\n  2\n ],\n \"c\": {},\n \"d\": [],\n \"e\": \"é\"\n}"
	got, err := Indent1([]byte(`{"a":1,"b":[1,2],"c":{},"d":[],"e":"é"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestMarshalNoHTMLEscapeNoNewline(t *testing.T) {
	b, err := Marshal(map[string]any{"p": "a&b<c>", "d": Float(40)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"d":40.0,"p":"a&b<c>"}` {
		t.Errorf("got %s", b)
	}
}
