package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"codebase.bid/lib/go/oauthas"
)

// RotateKey atomically replaces the active key, preserving one bounded previous
// key. The operator supplies the printed overlap expiry to the next serve unit.
func RotateKey(active, previous string) (time.Time, error) {
	if active == previous || !filepath.IsAbs(active) || !filepath.IsAbs(previous) {
		return time.Time{}, fmt.Errorf("distinct absolute key paths are required")
	}
	info, e := os.Lstat(active)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return time.Time{}, fmt.Errorf("active key must be a private regular file")
	}
	old, e := os.ReadFile(active)
	if e != nil || len(old) < 32 || len(old) > 65536 {
		return time.Time{}, fmt.Errorf("invalid active signing key")
	}
	next, e := oauthas.RandomToken()
	if e != nil {
		return time.Time{}, e
	}
	write := func(path string, data []byte) error {
		f, e := os.CreateTemp(filepath.Dir(path), ".memory-key-")
		if e != nil {
			return e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(data); e != nil {
			f.Close()
			return e
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if e = os.Rename(f.Name(), path); e != nil {
			return e
		}
		dir, e := os.Open(filepath.Dir(path))
		if e != nil {
			return e
		}
		defer dir.Close()
		return dir.Sync()
	}
	if e = write(previous, old); e != nil {
		return time.Time{}, e
	}
	if e = write(active, []byte(next)); e != nil {
		return time.Time{}, e
	}
	return time.Now().UTC().Add(15 * time.Minute), nil
}
