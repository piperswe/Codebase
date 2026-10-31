package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateAdminAndSecrets(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8081", "example.com:8081", "unix:relative"} {
		if privateAddress(addr) == nil {
			t.Fatal("public admin accepted", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8081", "[::1]:8081", "unix:/run/memory/admin.sock"} {
		if e := privateAddress(addr); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(t.TempDir(), "key")
	if e := os.WriteFile(path, []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("MEMORY_TEST_SECRET_FILE", path)
	t.Setenv("MEMORY_TEST_SECRET", "conflict")
	if _, e := secret("MEMORY_TEST_SECRET"); e == nil {
		t.Fatal("conflicting settings accepted")
	}
	t.Setenv("MEMORY_TEST_SECRET", "")
	if v, e := secret("MEMORY_TEST_SECRET"); e != nil || v != "secret" {
		t.Fatal(v, e)
	}
	os.Chmod(path, 0644)
	if _, e := secret("MEMORY_TEST_SECRET"); e == nil {
		t.Fatal("public secret accepted")
	}
}
func TestWorkerRejectsCredentialsAndRemoteIPC(t *testing.T) {
	for _, name := range []string{"MEMORY_DB_DSN", "MEMORY_READER_DSN", "MEMORY_MIGRATOR_DSN", "MEMORY_OWNER_HASH", "MEMORY_SIGNING_KEY", "MEMORY_PREVIOUS_KEY", "MEMORY_CURSOR_KEY", "GITHUB_TOKEN", "GH_TOKEN", "AWS_ACCESS_KEY_ID", "TAILSCALE_AUTHKEY", "BAZEL_CACHE_PASSWORD"} {
		t.Setenv(name, "")
		t.Setenv(name+"_FILE", "")
	}
	if _, e := Load("worker"); e != nil {
		t.Fatal("positive credential-free worker", e)
	}
	for _, name := range []string{"MEMORY_DB_DSN", "MEMORY_OWNER_HASH_FILE", "GITHUB_TOKEN"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "forbidden")
			if _, e := Load("worker"); e == nil {
				t.Fatal("worker accepted credential", name)
			}
		})
	}
	t.Setenv("MEMORY_WORKER_SOCKET", "https://remote.example/worker")
	if _, e := Load("worker"); e == nil {
		t.Fatal("remote IPC accepted")
	}
}
func TestDatabaseTransportPolicy(t *testing.T) {
	for _, dsn := range []string{"postgres://app@example.com/memory?sslmode=disable", "postgres://app@example.com/memory?sslmode=require"} {
		if validateDSN(dsn) == nil {
			t.Fatal("unverified remote DB accepted")
		}
	}
	for _, dsn := range []string{"host=/run/postgresql dbname=memory user=memory_app", "postgres://app@127.0.0.1/memory?sslmode=disable", "postgres://app@example.com/memory?sslmode=verify-full"} {
		if e := validateDSN(dsn); e != nil {
			t.Fatal(e)
		}
	}
}
