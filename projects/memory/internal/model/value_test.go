package model

import (
	"encoding/json"
	"testing"
)

func TestCanonicalValues(t *testing.T) {
	for _, x := range []struct {
		typ      ValueType
		in, want string
	}{{Integer, "-9223372036854775808", "-9223372036854775808"}, {Integer, "000", "0"}, {Decimal, "-001.2300", "-1.23"}, {Decimal, "-0.00", "0"}, {Timestamp, "2026-01-01T01:00:00.123456+01:00", "2026-01-01T00:00:00.123456Z"}, {String, "e\u0301", "e\u0301"}, {String, "", ""}} {
		t.Run(string(x.typ)+x.in, func(t *testing.T) {
			v, e := (Value{Type: x.typ, Text: x.in}).Canonical()
			if e != nil || v.Text != x.want {
				t.Fatal(v, e)
			}
		})
	}
}
func TestInvalidValues(t *testing.T) {
	for _, v := range []Value{{Type: Integer, Text: "9223372036854775808"}, {Type: Integer, Text: "1.0"}, {Type: Decimal, Text: "1e3"}, {Type: Decimal, Text: "0.0000000000001"}, {Type: Decimal, Text: "999999999999999999999999999999999999999"}, {Type: Timestamp, Text: "2026-01-01T00:00:00.0000001Z"}, {Type: Timestamp, Text: "2026-01-01T00:00:00"}, {Type: String, Text: "\x00"}, {Type: "unknown", Text: "x"}, {Type: Ref, Text: "other/thing"}} {
		if _, e := v.Canonical(); e == nil {
			t.Fatal("accepted", v)
		}
	}
}
func TestWireUnion(t *testing.T) {
	for _, s := range []string{`{"type":"integer","value":123}`, `{"type":"boolean","value":"false"}`, `{"type":"string","value":null}`, `{"type":"string","value":"ok","extra":true}`} {
		var v Value
		if json.Unmarshal([]byte(s), &v) == nil {
			t.Fatal("accepted", s)
		}
	}
	for _, s := range []string{`{"type":"boolean","value":false}`, `{"type":"integer","value":"0"}`, `{"type":"string","value":""}`} {
		var v Value
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(v)
		if string(b) != s {
			t.Fatal(string(b))
		}
	}
}
func TestStableIdentityAndFingerprint(t *testing.T) {
	space := "132f9270-0f3f-4ba5-a50e-329d360b8a77"
	if StableID(space, "entity/type") != StableID(space, "entity/type") {
		t.Fatal("unstable")
	}
	a, _ := (Value{Type: Decimal, Text: "1.00"}).Canonical()
	b, _ := (Value{Type: Decimal, Text: "1"}).Canonical()
	if string(a.Fingerprint()) != string(b.Fingerprint()) {
		t.Fatal("decimal equality changed")
	}
}
