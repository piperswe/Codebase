// Package model defines the lossless, versioned Memory wire contracts.
package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ValueType string

const (
	String    ValueType = "string"
	Boolean   ValueType = "boolean"
	Integer   ValueType = "integer"
	Decimal   ValueType = "decimal"
	Timestamp ValueType = "timestamp"
	Ref       ValueType = "ref"
)

// Value has no floating point representation for exact numeric values.
// Text is used for every type except Boolean. JSON always uses a tagged union.
type Value struct {
	Type ValueType
	Text string
	Bool bool
}

func (v Value) MarshalJSON() ([]byte, error) {
	var x any = v.Text
	if v.Type == Boolean {
		x = v.Bool
	}
	return json.Marshal(struct {
		Type  ValueType `json:"type"`
		Value any       `json:"value"`
	}{v.Type, x})
}

func (v *Value) UnmarshalJSON(b []byte) error {
	var w struct {
		Type  ValueType       `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := Decode(b, &w); err != nil {
		return err
	}
	if len(w.Value) == 0 || bytes.Equal(w.Value, []byte("null")) {
		return E("INVALID_VALUE_TYPE", "A fact value cannot be null")
	}
	v.Type = w.Type
	if w.Type == Boolean {
		if err := json.Unmarshal(w.Value, &v.Bool); err != nil {
			return E("INVALID_VALUE_TYPE", "Boolean requires a JSON boolean")
		}
	} else if err := json.Unmarshal(w.Value, &v.Text); err != nil {
		return E("INVALID_VALUE_TYPE", "This value type requires a JSON string")
	}
	if v.Type == Ref && (ValidName(v.Text) || (strings.HasPrefix(v.Text, "temp:") && len(v.Text) > 5 && len(v.Text) <= 105)) {
		return nil
	} // resolved inside the authorized space
	n, err := v.Canonical()
	if err == nil {
		*v = n
	}
	return err
}

var decimalPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*/[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func ValidName(s string) bool { return len(s) <= 200 && namePattern.MatchString(s) }
func Reserved(s string) bool {
	return strings.HasPrefix(s, "sys/") || strings.HasPrefix(s, "schema/") || strings.HasPrefix(s, "entity/")
}

func (v Value) Canonical() (Value, error) {
	if v.Type != Boolean {
		v.Bool = false
	}
	bad := func() (Value, error) { return Value{}, E("INVALID_VALUE_TYPE", "Invalid "+string(v.Type)+" value") }
	switch v.Type {
	case String:
		if !utf8.ValidString(v.Text) || len(v.Text) > 16384 || strings.ContainsRune(v.Text, 0) {
			return bad()
		}
	case Boolean:
		v.Text = ""
	case Integer:
		if strings.HasPrefix(v.Text, "+") {
			return bad()
		}
		n, err := strconv.ParseInt(v.Text, 10, 64)
		if err != nil {
			return bad()
		}
		v.Text = strconv.FormatInt(n, 10)
	case Decimal:
		if !decimalPattern.MatchString(v.Text) || len(v.Text) > 128 {
			return bad()
		}
		neg := strings.HasPrefix(v.Text, "-")
		s := strings.TrimPrefix(v.Text, "-")
		p := strings.SplitN(s, ".", 2)
		whole := strings.TrimLeft(p[0], "0")
		if whole == "" {
			whole = "0"
		}
		frac := ""
		if len(p) == 2 {
			frac = strings.TrimRight(p[1], "0")
		}
		digits := strings.TrimLeft(whole+frac, "0")
		if len(digits) > 38 || len(frac) > 12 {
			return bad()
		}
		v.Text = whole
		if frac != "" {
			v.Text += "." + frac
		}
		if neg && v.Text != "0" {
			v.Text = "-" + v.Text
		}
	case Timestamp:
		t, err := time.Parse(time.RFC3339Nano, v.Text)
		if err != nil || t.Nanosecond()%1000 != 0 || t.Year() < 1 || t.Year() > 9999 {
			return bad()
		}
		v.Text = t.UTC().Format(time.RFC3339Nano)
	case Ref:
		id, err := uuid.Parse(v.Text)
		if err != nil {
			return bad()
		}
		v.Text = id.String()
	default:
		return bad()
	}
	return v, nil
}

func (v Value) Fingerprint() []byte {
	b, _ := v.MarshalJSON()
	s := sha256.Sum256(append([]byte("memory-value-v1\x00"), b...))
	return s[:]
}
func (v Value) Equal(w Value) bool { return v.Type == w.Type && v.Text == w.Text && v.Bool == w.Bool }
func Hash(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func Decode(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return E("INVALID_INPUT", "Invalid or unknown input field")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return E("INVALID_INPUT", "Expected one JSON value")
	}
	return nil
}
func Revision(n int64) string { return strconv.FormatInt(n, 10) }
func ParseRevision(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 {
		return 0, E("INVALID_INPUT", "Invalid revision")
	}
	return n, nil
}
func NewID() string { return uuid.NewString() }
func StableID(space, ident string) string {
	return uuid.NewSHA1(uuid.MustParse(space), []byte(ident)).String()
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

func (e *Error) Error() string      { return e.Code + ": " + e.Message }
func E(code, message string) *Error { return &Error{Code: code, Message: message} }
func Limit(s string) *Error         { return E("QUERY_LIMIT_EXCEEDED", s) }
func Require(ok bool, code, message string) error {
	if !ok {
		return E(code, message)
	}
	return nil
}
func Invalid(field string) error { return E("INVALID_INPUT", fmt.Sprintf("Invalid %s", field)) }
