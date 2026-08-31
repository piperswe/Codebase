package pwhash

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/crypto/argon2"
)

var _ PasswordHasher = (*argon2idHasher)(nil)

func testArgon2idParams() argon2idParams {
	return argon2idParams{
		Time:    1,
		Memory:  64,
		Threads: 1,
		KeyLen:  32,
	}
}

func newTestHasher() *argon2idHasher {
	params := testArgon2idParams()
	return &argon2idHasher{params: &params}
}

func decodeGeneratedHash(t *testing.T, encoded string) ([]byte, []byte) {
	t.Helper()

	salt, key, err := decodeArgon2idHash(encoded)
	if err != nil {
		t.Fatalf("decode generated hash: %v", err)
	}
	return salt, key
}

func TestNew(t *testing.T) {
	hasher := New()
	if hasher == nil {
		t.Fatal("New returned nil")
	}
	if _, ok := hasher.(*argon2idHasher); !ok {
		t.Fatalf("New returned %T, want *argon2idHasher", hasher)
	}
}

func TestArgon2idParameters(t *testing.T) {
	wantDefault := argon2idParams{
		Time:    1,
		Memory:  2 * 1024 * 1024,
		Threads: 4,
		KeyLen:  32,
	}
	if rfc9106Section4Params != wantDefault {
		t.Fatalf("RFC 9106 parameters = %+v, want %+v", rfc9106Section4Params, wantDefault)
	}
	if got := (&argon2idHasher{}).parameters(); got != &rfc9106Section4Params {
		t.Fatalf("nil parameter override resolved to %p, want RFC parameters at %p", got, &rfc9106Section4Params)
	}

	custom := testArgon2idParams()
	if got := (&argon2idHasher{params: &custom}).parameters(); got != &custom {
		t.Fatalf("custom parameter override resolved to %p, want %p", got, &custom)
	}
}

func TestHashAndCompare(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "ASCII", password: "correct horse battery staple"},
		{name: "empty", password: ""},
		{name: "Unicode", password: "pässwörd 🔐"},
		{name: "embedded NUL", password: "before\x00after"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasher := newTestHasher()
			encoded, err := hasher.Hash(tt.password)
			if err != nil {
				t.Fatalf("Hash: %v", err)
			}

			salt, key := decodeGeneratedHash(t, encoded)
			if len(salt) != 16 {
				t.Errorf("salt length = %d, want 16", len(salt))
			}
			if len(key) != int(hasher.params.KeyLen) {
				t.Errorf("key length = %d, want %d", len(key), hasher.params.KeyLen)
			}
			wantKey := argon2.IDKey(
				[]byte(tt.password),
				salt,
				hasher.params.Time,
				hasher.params.Memory,
				hasher.params.Threads,
				hasher.params.KeyLen,
			)
			if !bytes.Equal(key, wantKey) {
				t.Errorf("generated key = %x, want %x", key, wantKey)
			}
			if err := hasher.Compare(encoded, tt.password); err != nil {
				t.Errorf("Compare generated hash: %v", err)
			}
		})
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	hasher := newTestHasher()
	first, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("first Hash: %v", err)
	}
	second, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("second Hash: %v", err)
	}

	firstSalt, firstKey := decodeGeneratedHash(t, first)
	secondSalt, secondKey := decodeGeneratedHash(t, second)
	if bytes.Equal(firstSalt, secondSalt) {
		t.Errorf("successive hashes used the same salt %x", firstSalt)
	}
	if bytes.Equal(firstKey, secondKey) {
		t.Errorf("successive hashes produced the same key %x", firstKey)
	}
	if first == second {
		t.Errorf("successive encoded hashes are identical: %q", first)
	}
}

func TestCompareKnownArgon2idVector(t *testing.T) {
	const encoded = `{"Hash":"92dc5d67019623868bde079275e522f4b7e8213d3414ed85cbc2ac8a41117288","Salt":"000102030405060708090a0b0c0d0e0f"}`
	hasher := newTestHasher()

	if err := hasher.Compare(encoded, "correct horse battery staple"); err != nil {
		t.Fatalf("Compare known vector: %v", err)
	}
	if err := hasher.Compare(encoded, "wrong password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Compare wrong password error = %v, want ErrPasswordMismatch", err)
	}
}

func TestCompareRejectsMismatches(t *testing.T) {
	const salt = "000102030405060708090a0b0c0d0e0f"
	const hash = "92dc5d67019623868bde079275e522f4b7e8213d3414ed85cbc2ac8a41117288"
	tests := []struct {
		name     string
		encoded  string
		password string
	}{
		{name: "wrong password", encoded: `{"Hash":"` + hash + `","Salt":"` + salt + `"}`, password: "incorrect"},
		{name: "modified salt", encoded: `{"Hash":"` + hash + `","Salt":"100102030405060708090a0b0c0d0e0f"}`, password: "correct horse battery staple"},
		{name: "modified hash", encoded: `{"Hash":"82dc5d67019623868bde079275e522f4b7e8213d3414ed85cbc2ac8a41117288","Salt":"` + salt + `"}`, password: "correct horse battery staple"},
		{name: "truncated hash", encoded: `{"Hash":"` + hash[:len(hash)-2] + `","Salt":"` + salt + `"}`, password: "correct horse battery staple"},
		{name: "missing hash", encoded: `{"Salt":"` + salt + `"}`, password: "correct horse battery staple"},
		{name: "missing salt", encoded: `{"Hash":"` + hash + `"}`, password: "correct horse battery staple"},
	}

	hasher := newTestHasher()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasher.Compare(tt.encoded, tt.password)
			if !errors.Is(err, ErrPasswordMismatch) {
				t.Fatalf("Compare error = %v, want ErrPasswordMismatch", err)
			}
		})
	}
}

func TestCompareRejectsMalformedHashes(t *testing.T) {
	const salt = "000102030405060708090a0b0c0d0e0f"
	const hash = "92dc5d67019623868bde079275e522f4b7e8213d3414ed85cbc2ac8a41117288"
	tests := []struct {
		name    string
		encoded string
	}{
		{name: "invalid JSON", encoded: "{"},
		{name: "JSON array", encoded: "[]"},
		{name: "incorrect field type", encoded: `{"Hash":1,"Salt":"` + salt + `"}`},
		{name: "trailing data", encoded: `{"Hash":"` + hash + `","Salt":"` + salt + `"} trailing`},
		{name: "invalid salt hex", encoded: `{"Hash":"` + hash + `","Salt":"not-hex"}`},
		{name: "odd-length salt hex", encoded: `{"Hash":"` + hash + `","Salt":"0"}`},
		{name: "invalid hash hex", encoded: `{"Hash":"not-hex","Salt":"` + salt + `"}`},
		{name: "odd-length hash hex", encoded: `{"Hash":"0","Salt":"` + salt + `"}`},
	}

	hasher := newTestHasher()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasher.Compare(tt.encoded, "correct horse battery staple")
			if err == nil {
				t.Fatal("Compare returned nil, want parsing error")
			}
			if errors.Is(err, ErrPasswordMismatch) {
				t.Fatalf("Compare error = %v, want parsing error", err)
			}
		})
	}
}
