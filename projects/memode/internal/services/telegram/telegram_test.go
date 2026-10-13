package telegram

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
)

func TestArgHelpers(t *testing.T) {
	if got := argInt(map[string]any{"n": float64(7)}, "n", 1); got != 7 {
		t.Fatalf("argInt float: %d", got)
	}
	if got := argInt(map[string]any{}, "n", 3); got != 3 {
		t.Fatalf("argInt default: %d", got)
	}
	ids, err := argIDs(float64(5))
	if err != nil || len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("argIDs single: %v %v", ids, err)
	}
	ids, err = argIDs([]any{float64(1), float64(2)})
	if err != nil || len(ids) != 2 {
		t.Fatalf("argIDs list: %v %v", ids, err)
	}
	if _, err := argIDs("nope"); err == nil {
		t.Fatal("argIDs should reject strings")
	}
	if ids, err := argIDs(nil); err != nil || ids != nil {
		t.Fatalf("argIDs nil: %v %v", ids, err)
	}
}

func TestPeerIDAndKind(t *testing.T) {
	if peerID(&tg.PeerUser{UserID: 11}) != 11 || peerID(&tg.PeerChat{ChatID: 22}) != 22 || peerID(&tg.PeerChannel{ChannelID: 33}) != 33 {
		t.Fatal("peerID mismatch")
	}
	if messageKind(&tg.Message{}) != "text" {
		t.Fatal("plain message should be text")
	}
	if messageKind(&tg.Message{Media: &tg.MessageMediaPhoto{}}) != "photo" {
		t.Fatal("photo message kind")
	}
	if messageKind(&tg.Message{Media: &tg.MessageMediaDocument{}}) != "document" {
		t.Fatal("document message kind")
	}
}

func TestMessagesToLua(t *testing.T) {
	withFrom := &tg.Message{ID: 1, Date: 1700000000, Message: "hello", Out: true,
		FromID:  &tg.PeerUser{UserID: 42},
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 9}}
	withFrom.SetFlags() // generated structs carry flag bits for optional fields
	res := &tg.MessagesMessages{Messages: []tg.MessageClass{
		withFrom,
		&tg.MessageService{ID: 2, Date: 1700000001},
	}}
	out := messagesToLua(res, nil)
	if len(out) != 1 {
		t.Fatalf("service messages should be skipped, got %d", len(out))
	}
	m := out[0]
	if m["message_id"] != 1 || m["text"] != "hello" || m["outgoing"] != true || m["kind"] != "text" {
		t.Fatalf("message fields: %#v", m)
	}
	if m["sender_id"] != int64(42) || m["reply_to"] != 9 {
		t.Fatalf("sender/reply: %#v", m)
	}
	if out2 := messagesToLua(&tg.MessagesMessages{}, nil); len(out2) != 0 {
		t.Fatalf("empty result should be an empty slice, got %#v", out2)
	}
}

func TestResolvePeerCache(t *testing.T) {
	cfg := config.Config{}
	svc := New(cfg)
	// Warm the cache the way fnDialogs would.
	alice := &tg.User{ID: 1, AccessHash: 99, FirstName: "Alice", Username: "alice"}
	alice.SetFlags()
	channel := &tg.Channel{ID: 3, AccessHash: 77, Title: "Orchard News", Username: "orchardnews"}
	channel.SetFlags()
	svc.cachePeers(
		[]tg.UserClass{alice},
		[]tg.ChatClass{&tg.Chat{ID: 2, Title: "Fruit Lovers"}, channel},
	)

	for _, tc := range []struct{ arg, want string }{
		{"1", "1"}, {"@alice", "1"}, {"@ALICE", "1"},
		{"2", "2"}, {"3", "3"}, {"@orchardnews", "3"},
	} {
		peer, err := svc.resolvePeer(context.Background(), nil, tc.arg)
		if err != nil {
			t.Fatalf("resolve %q: %v", tc.arg, err)
		}
		if peer == nil {
			t.Fatalf("resolve %q: nil peer", tc.arg)
		}
	}
	// Title substring works when unambiguous.
	if _, err := svc.resolvePeer(context.Background(), nil, "orchard"); err != nil {
		t.Fatalf("substring resolve: %v", err)
	}
	// Unknown chat reports an error (with no API client, the refresh fails).
	if _, err := svc.resolvePeer(context.Background(), nil, "nobody"); err == nil {
		t.Fatal("unknown chat should fail")
	}
}

func TestUnavailableSession(t *testing.T) {
	svc := New(config.Config{})
	_, err := svc.client()
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected session-not-connected error, got %v", err)
	}
	// Script-facing calls must return errors rather than panicking.
	reg := &codemode.Registry{}
	svc.Register(reg)
	res, err := codemode.Run(context.Background(), reg, codemode.AllowAll, `return telegram.dialogs({})`, codemode.RunOptions{})
	if err == nil {
		t.Fatalf("expected error, got %v", res.Items)
	}
}

func TestRegisterExposesReadAndWrite(t *testing.T) {
	reg := &codemode.Registry{}
	New(config.Config{}).Register(reg)
	var reads, writes int
	for _, f := range reg.Functions() {
		if f.Namespace != "telegram" {
			t.Fatalf("unexpected namespace %q", f.Namespace)
		}
		if f.Write {
			writes++
		} else {
			reads++
		}
	}
	if reads == 0 || writes == 0 {
		t.Fatalf("expected read and write functions, got %d/%d", reads, writes)
	}
	// The read-only environment must not see the write functions.
	res, err := codemode.Run(context.Background(), reg, codemode.AllowReadOnly, `
		return tostring(telegram.send == nil) .. "/" .. type(telegram.history)
	`, codemode.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items[0] != "true/function" {
		t.Fatalf("read-only env exposed: %s", res.Items[0])
	}
}

// TestTelegramSmoke connects with real credentials and lists dialogs. It is
// skipped unless MEMODE_TELEGRAM_TESTS=1 and full credentials are present.
func TestTelegramSmoke(t *testing.T) {
	if os.Getenv("MEMODE_TELEGRAM_TESTS") != "1" {
		t.Skip("set MEMODE_TELEGRAM_TESTS=1 with real Telegram credentials to run")
	}
	cfg := config.Config{
		TelegramAPIID:   os.Getenv("MEMODE_TELEGRAM_API_ID"),
		TelegramAPIHash: os.Getenv("MEMODE_TELEGRAM_API_HASH"),
		TelegramPhone:   os.Getenv("MEMODE_TELEGRAM_PHONE"),
		DBPath:          t.TempDir() + "/smoke.db",
	}
	if cfg.TelegramAPIID == "" || cfg.TelegramAPIHash == "" || cfg.TelegramPhone == "" {
		t.Skip("telegram credentials not configured")
	}
	svc := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Setup(ctx, func(bool) {})

	deadline := time.Now().Add(2 * time.Minute)
	for !svc.isReady() {
		if time.Now().After(deadline) {
			t.Skip("telegram session did not become ready (interactive login required)")
		}
		time.Sleep(500 * time.Millisecond)
	}
	dialogs, err := svc.fnDialogs(ctx, map[string]any{"limit": float64(5)})
	if err != nil {
		t.Fatalf("dialogs: %v", err)
	}
	t.Logf("dialogs: %d", len(dialogs.([]map[string]any)))
}
