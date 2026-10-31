package cursor

import (
	"strings"
	"testing"
	"time"
)

func TestSignedCursor(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	s := State{Space: "space", Query: "query", Last: "last", Data: 7, Schema: 2}
	token, e := Encode(key, s)
	if e != nil {
		t.Fatal(e)
	}
	got, e := Decode(key, token)
	if e != nil || got.Space != s.Space || got.Data != s.Data {
		t.Fatal(got, e)
	}
	for _, bad := range []string{token + "x", strings.Replace(token, ".", "x", 1), "invalid"} {
		if _, e = Decode(key, bad); e == nil {
			t.Fatal("forgery accepted")
		}
	}
	s.Expires = time.Now().Add(-time.Second).Unix()
	token, _ = Encode(key, s)
	if _, e = Decode(key, token); e == nil {
		t.Fatal("expired cursor accepted")
	}
}
