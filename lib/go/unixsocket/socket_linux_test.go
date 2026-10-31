package unixsocket

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedSocketLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.sock")
	l, e := Listen(path, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if _, e = Listen(path, os.Getuid()); e == nil {
		t.Fatal("second listener replaced socket")
	}
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			c.Close()
		}
		done <- e
	}()
	c, e := Dial(t.Context(), path, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestRefuseFileAndSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "worker.sock")
		target := filepath.Join(dir, "target")
		if e := os.WriteFile(target, []byte("keep"), 0600); e != nil {
			t.Fatal(e)
		}
		if symlink {
			if e := os.Symlink(target, path); e != nil {
				t.Fatal(e)
			}
		} else {
			if e := os.WriteFile(path, []byte("keep"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := Listen(path, -1); e == nil {
			t.Fatal("unsafe path accepted")
		}
		b, e := os.ReadFile(target)
		if e != nil || string(b) != "keep" {
			t.Fatal("target changed")
		}
	}
}
