// Package unixsocket provides an owned, locked Unix listener and optional Linux
// peer-UID checks. It never removes a symlink or somebody else's socket.
package unixsocket

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Listener struct {
	*net.UnixListener
	lock *os.File
	peer int
}

func Listen(path string, peerUID int) (*Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("socket path must be absolute")
	}
	parent, e := os.Lstat(filepath.Dir(path))
	if e != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0002 != 0 {
		return nil, fmt.Errorf("socket requires an existing non-world-writable directory")
	}
	fd, e := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	fail := func(err error) (*Listener, error) { lock.Close(); return nil, err }
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fail(fmt.Errorf("socket already locked"))
	}
	info, e := os.Lstat(path)
	if e == nil {
		// FileInfo.Sys uses syscall.Stat_t; fstatat avoids a platform-specific cast.
		var st unix.Stat_t
		if unix.Lstat(path, &st) != nil || info.Mode()&os.ModeSocket == 0 || st.Uid != uint32(os.Getuid()) {
			return fail(fmt.Errorf("existing path is not an owned socket"))
		}
		if e = os.Remove(path); e != nil {
			return fail(e)
		}
	} else if !os.IsNotExist(e) {
		return fail(e)
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return fail(e)
	}
	if e = os.Chmod(path, 0660); e != nil {
		l.Close()
		return fail(e)
	}
	return &Listener{UnixListener: l, lock: lock, peer: peerUID}, nil
}
func peer(c *net.UnixConn, uid int) error {
	if uid < 0 {
		return nil
	}
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var check error
	e = raw.Control(func(fd uintptr) {
		u, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil || u.Uid != uint32(uid) {
			check = fmt.Errorf("unexpected Unix peer identity")
		}
	})
	if e != nil {
		return e
	}
	return check
}
func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, e := l.AcceptUnix()
		if e != nil {
			return nil, e
		}
		if e = peer(c, l.peer); e != nil {
			c.Close()
			continue
		}
		return c, nil
	}
}
func (l *Listener) Close() error { e := l.UnixListener.Close(); _ = l.lock.Close(); return e }
func Dial(ctx context.Context, path string, uid int) (net.Conn, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("socket path must be absolute")
	}
	c, e := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if e != nil {
		return nil, e
	}
	if e = peer(c.(*net.UnixConn), uid); e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}
