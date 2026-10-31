// Package config reads explicit MEMORY settings. It never loads .env files.
package config

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Config struct {
	AppPoolMax, ReaderPoolMax                                                       int
	CodeTimeoutMS, GraphOperations, ResultBytes, LogBytes                           int
	PublicURL, HTTP, Admin, WorkerSocket, AppDSN, ReaderDSN, MigratorDSN, OwnerHash string
	Key, CursorKey, PreviousKey                                                     []byte
	PreviousUntil                                                                   time.Time
	PeerUID                                                                         int
	DynamicRegistration                                                             bool
}

func secret(name string) (string, error) {
	value, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && path != "" {
		return "", fmt.Errorf("conflicting %s and %s_FILE", name, name)
	}
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return "", fmt.Errorf("cannot read %s_FILE", name)
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 {
			return "", fmt.Errorf("%s_FILE must be a regular non-public secret file", name)
		}
		b, e := io.ReadAll(io.LimitReader(f, 65537))
		if e != nil || len(b) > 65536 {
			return "", fmt.Errorf("%s_FILE exceeds limit", name)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return value, nil
}
func get(name, def string) string {
	if x := os.Getenv(name); x != "" {
		return x
	}
	return def
}
func Load(mode string) (Config, error) {
	c := Config{PublicURL: os.Getenv("MEMORY_PUBLIC_URL"), HTTP: get("MEMORY_HTTP_ADDR", "127.0.0.1:8080"), Admin: get("MEMORY_ADMIN_ADDR", "127.0.0.1:8081"), WorkerSocket: get("MEMORY_WORKER_SOCKET", "/run/structured-memory/worker.sock"), PeerUID: -1}
	if v := os.Getenv("MEMORY_PEER_UID"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 {
			return c, fmt.Errorf("invalid MEMORY_PEER_UID")
		}
		c.PeerUID = n
	}
	if !filepath.IsAbs(c.WorkerSocket) {
		return c, fmt.Errorf("MEMORY_WORKER_SOCKET must be absolute")
	}
	c.AppPoolMax = 8
	c.ReaderPoolMax = 2
	c.CodeTimeoutMS = 5000
	c.GraphOperations = 100
	c.ResultBytes = 262144
	c.LogBytes = 16384
	for _, opt := range []struct {
		name string
		max  int
		dst  *int
	}{{"MEMORY_APP_POOL_MAX", 64, &c.AppPoolMax}, {"MEMORY_READER_POOL_MAX", 16, &c.ReaderPoolMax}, {"MEMORY_CODE_TIMEOUT_MS", 5000, &c.CodeTimeoutMS}, {"MEMORY_CODE_OPERATIONS", 100, &c.GraphOperations}, {"MEMORY_CODE_RESULT_BYTES", 262144, &c.ResultBytes}, {"MEMORY_CODE_LOG_BYTES", 16384, &c.LogBytes}} {
		if text := os.Getenv(opt.name); text != "" {
			n, e := strconv.Atoi(text)
			if e != nil || n < 1 || n > opt.max {
				return c, fmt.Errorf("invalid %s", opt.name)
			}
			*opt.dst = n
		}
	}
	if mode == "worker" {
		for _, name := range []string{"MEMORY_DB_DSN", "MEMORY_READER_DSN", "MEMORY_MIGRATOR_DSN", "MEMORY_OWNER_HASH", "MEMORY_SIGNING_KEY", "MEMORY_PREVIOUS_KEY", "MEMORY_CURSOR_KEY", "GITHUB_TOKEN", "GH_TOKEN", "AWS_ACCESS_KEY_ID", "TAILSCALE_AUTHKEY", "BAZEL_CACHE_PASSWORD"} {
			if os.Getenv(name) != "" || os.Getenv(name+"_FILE") != "" {
				return c, fmt.Errorf("worker must not receive %s credentials", name)
			}
		}
		return c, nil
	}
	var e error
	if mode == "migrate" {
		c.MigratorDSN, e = secret("MEMORY_MIGRATOR_DSN")
		if e != nil {
			return c, e
		}
		if c.MigratorDSN == "" {
			return c, fmt.Errorf("MEMORY_MIGRATOR_DSN_FILE required")
		}
		return c, validateDSN(c.MigratorDSN)
	}
	if mode == "healthcheck" {
		return c, privateAddress(c.Admin)
	}
	c.AppDSN, e = secret("MEMORY_DB_DSN")
	if e != nil {
		return c, e
	}
	c.ReaderDSN, e = secret("MEMORY_READER_DSN")
	if e != nil {
		return c, e
	}
	key, e := secret("MEMORY_CURSOR_KEY")
	if e != nil {
		return c, e
	}
	c.CursorKey = []byte(key)
	if c.AppDSN == "" || c.ReaderDSN == "" || len(c.CursorKey) < 32 {
		return c, fmt.Errorf("application/reader DSNs and persisted cursor key files are required")
	}
	if e = validateDSN(c.AppDSN); e != nil {
		return c, e
	}
	if e = validateDSN(c.ReaderDSN); e != nil {
		return c, e
	}
	if mode != "serve" {
		return c, nil
	}
	if os.Getenv("MEMORY_MIGRATOR_DSN") != "" || os.Getenv("MEMORY_MIGRATOR_DSN_FILE") != "" {
		return c, fmt.Errorf("serve must not receive migrator credentials")
	}
	c.OwnerHash, e = secret("MEMORY_OWNER_HASH")
	if e != nil {
		return c, e
	}
	key, e = secret("MEMORY_SIGNING_KEY")
	if e != nil {
		return c, e
	}
	c.Key = []byte(key)
	key, e = secret("MEMORY_PREVIOUS_KEY")
	if e != nil {
		return c, e
	}
	c.PreviousKey = []byte(key)
	if x := os.Getenv("MEMORY_PREVIOUS_KEY_UNTIL"); x != "" {
		c.PreviousUntil, e = time.Parse(time.RFC3339, x)
		if e != nil {
			return c, fmt.Errorf("invalid previous key expiry")
		}
	}
	if x := os.Getenv("MEMORY_DYNAMIC_REGISTRATION"); x != "" {
		c.DynamicRegistration, e = strconv.ParseBool(x)
		if e != nil {
			return c, fmt.Errorf("invalid registration setting")
		}
	}
	return c, privateAddress(c.Admin)
}
func privateAddress(addr string) error {
	if strings.HasPrefix(addr, "unix:") {
		if filepath.IsAbs(strings.TrimPrefix(addr, "unix:")) {
			return nil
		}
		return fmt.Errorf("admin Unix socket must be absolute")
	}
	host, _, e := net.SplitHostPort(addr)
	if e != nil {
		return fmt.Errorf("invalid admin address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("admin listener must use a literal loopback address or Unix socket")
	}
	return nil
}
func validateDSN(dsn string) error {
	c, e := pgx.ParseConfig(dsn)
	if e != nil {
		return fmt.Errorf("invalid database connection configuration")
	}
	if strings.HasPrefix(c.Host, "/") {
		return nil
	}
	ip := net.ParseIP(c.Host)
	if (ip != nil && ip.IsLoopback()) || c.Host == "localhost" {
		return nil
	}
	if c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify {
		return fmt.Errorf("nonlocal database requires certificate-verified TLS")
	}
	return nil
}
