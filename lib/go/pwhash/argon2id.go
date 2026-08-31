package pwhash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/argon2"
)

var ErrPasswordMismatch = fmt.Errorf("password does not match")

type argon2idParams struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}

var rfc9106Section4Params = argon2idParams{
	Time:    1,
	Memory:  2 * 1024 * 1024,
	Threads: 4,
	KeyLen:  32,
}

type argon2idHash struct {
	Hash string
	Salt string
}

type argon2idHasher struct {
	params *argon2idParams
}

func (h *argon2idHasher) parameters() *argon2idParams {
	if h.params == nil {
		return &rfc9106Section4Params
	}
	return h.params
}

func (h *argon2idHasher) Hash(password string) (string, error) {
	params := h.parameters()
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	if err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	hash := argon2idHash{
		Hash: hex.EncodeToString(key),
		Salt: hex.EncodeToString(salt),
	}
	encoded, err := json.Marshal(hash)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeArgon2idHash(encoded string) ([]byte, []byte, error) {
	hash := argon2idHash{}
	err := json.Unmarshal([]byte(encoded), &hash)
	if err != nil {
		return nil, nil, err
	}
	salt, err := hex.DecodeString(hash.Salt)
	if err != nil {
		return nil, nil, err
	}
	key, err := hex.DecodeString(hash.Hash)
	if err != nil {
		return nil, nil, err
	}
	return salt, key, nil
}

func (h *argon2idHasher) Compare(hashedPassword, password string) error {
	params := h.parameters()
	salt, key, err := decodeArgon2idHash(hashedPassword)
	if err != nil {
		return err
	}
	computedKey := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	if subtle.ConstantTimeCompare(computedKey, key) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}
