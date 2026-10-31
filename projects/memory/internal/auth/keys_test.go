package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeyRotation(t *testing.T) {
	dir := t.TempDir()
	active, previous := filepath.Join(dir, "active"), filepath.Join(dir, "previous")
	old := strings.Repeat("k", 32)
	if e := os.WriteFile(active, []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	until, e := RotateKey(active, previous)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := os.ReadFile(active)
	p, _ := os.ReadFile(previous)
	if string(p) != old || string(a) == old || len(a) < 32 || until.After(time.Now().Add(15*time.Minute)) {
		t.Fatal("invalid rotation")
	}
	if _, e = RotateKey(active, active); e == nil {
		t.Fatal("same key paths accepted")
	}
}
