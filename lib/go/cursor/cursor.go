// Package cursor signs bounded, expiring pagination state. The application
// supplies query/authority bindings and validates its revision semantics.
package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid or expired cursor")

type State struct {
	Version int    `json:"v"`
	Space   string `json:"s"`
	Query   string `json:"q"`
	Last    string `json:"l"`
	Data    int64  `json:"d"`
	Schema  int64  `json:"r"`
	Expires int64  `json:"e"`
}

func Encode(key []byte, s State) (string, error) {
	if len(key) < 32 {
		return "", ErrInvalid
	}
	s.Version = 1
	if s.Expires == 0 {
		s.Expires = time.Now().Add(15 * time.Minute).Unix()
	}
	b, e := json.Marshal(s)
	if e != nil {
		return "", e
	}
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}
func Decode(key []byte, token string) (State, error) {
	var s State
	if len(key) < 32 || len(token) > 4096 {
		return s, ErrInvalid
	}
	p := strings.Split(token, ".")
	if len(p) != 2 {
		return s, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return s, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return s, ErrInvalid
	}
	m := hmac.New(sha256.New, key)
	m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return s, ErrInvalid
	}
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Expires <= time.Now().Unix() {
		return s, ErrInvalid
	}
	return s, nil
}
