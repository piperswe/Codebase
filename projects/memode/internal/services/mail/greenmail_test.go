package mail

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/store"
)

// TestGreenmailRoundTrip runs a real IMAP/SMTP round trip against GreenMail:
// send → sync → search → set flags → move → delete, verifying the index after
// each mutation. Skipped with -short.
func TestGreenmailRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()

	gm, err := startGreenmail(ctx)
	if err != nil {
		t.Skipf("greenmail unavailable: %v", err)
	}
	t.Cleanup(func() { _ = gm.Terminate(ctx) })

	imapAddr := gm.Addr(ctx, t, "3993/tcp")
	smtpAddr := gm.Addr(ctx, t, "3025/tcp")
	adminURL := "http://" + gm.Addr(ctx, t, "8080/tcp")
	t.Setenv("MEMODE_IMAP_ADDR", imapAddr)
	t.Setenv("MEMODE_SMTP_ADDR", smtpAddr)
	t.Setenv("MEMODE_IMAP_INSECURE_TLS", "1")

	// Create the account GreenMail will serve.
	body := `{"email":"owner@example.com","login":"owner@example.com","password":"ownerpass"}`
	resp, err := http.Post(adminURL+"/api/user", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("create greenmail user: %v %v", err, resp)
	}
	resp.Body.Close()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Config{}
	cfg.ICloudUser, cfg.ICloudPassword = "owner@example.com", "ownerpass"
	svc := New(cfg, db)
	svc.Setup(ctx, func(bool) {})
	waitIndexed(t, svc, 0, "initial sync")

	// Send a mail: arrives in INBOX, fnSend re-syncs synchronously.
	if _, err := svc.fnSend(ctx, map[string]any{
		"to": "owner@example.com", "subject": "Roundtrip plans", "body": "we should visit the zucchini festival",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	rows := waitIndexed(t, svc, 1, "send reflected")
	id, _ := rows[0]["id"].(int64)

	// Full-text search finds it.
	res, err := svc.index.search("zucchini", "", 0, 0)
	if err != nil || len(res) != 1 || res[0]["subject"] != "Roundtrip plans" {
		t.Fatalf("search after send: %v %v", res, err)
	}

	// Flags: mark read, then confirm in the index.
	if _, err := svc.fnSetFlags(ctx, map[string]any{"id": float64(id), "seen": true}); err != nil {
		t.Fatalf("set flags: %v", err)
	}
	got, _ := svc.index.lookup(id)
	if !hasFlag(got.Flags, "\\Seen") {
		t.Fatalf("seen flag missing in index: %v", got.Flags)
	}

	// Move to a new mailbox; both mailboxes reflect the change.
	if err := svc.remote.createMailbox(ctx, "Festival"); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if _, err := svc.fnMove(ctx, map[string]any{"id": float64(id), "to": "Festival"}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if res, _ = svc.index.search("zucchini", "INBOX", 0, 0); len(res) != 0 {
		t.Fatalf("message still in INBOX after move")
	}
	if res, _ = svc.index.search("zucchini", "Festival", 0, 0); len(res) != 1 {
		t.Fatalf("message not in destination after move")
	}

	// Delete it: gone from the index.
	if _, err := svc.fnDelete(ctx, map[string]any{"id": float64(id)}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res, _ = svc.index.search("zucchini", "", 0, 0); len(res) != 0 {
		t.Fatalf("deleted message still searchable: %v", res)
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// waitIndexed polls until the index holds at least n messages.
func waitIndexed(t *testing.T, svc *Service, n int, stage string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := svc.index.recent("", 100)
		if err == nil && len(res) >= n {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: index never reached %d messages (have %d, err %v)", stage, n, len(res), err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// greenmail runs the GreenMail all-in-one container.
type greenmail struct {
	testcontainers.Container
}

func (g greenmail) Addr(ctx context.Context, t *testing.T, port string) string {
	t.Helper()
	host, err := g.Container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := g.Container.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	return host + ":" + mapped.Port()
}

func startGreenmail(ctx context.Context) (*greenmail, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "greenmail/standalone:2.1.4",
			ExposedPorts: []string{"3993/tcp", "3025/tcp", "8080/tcp"},
			WaitingFor:   wait.ForHTTP("/").WithPort("8080/tcp").WithStartupTimeout(2 * time.Minute),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return &greenmail{Container: c}, nil
}
