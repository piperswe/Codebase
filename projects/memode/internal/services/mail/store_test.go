package mail

import (
	"path/filepath"
	"strings"
	"testing"

	"codebase.bid/projects/memode/internal/store"
)

func testIndex(t *testing.T) index {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return index{db: db}
}

func msg(mailbox string, uid uint32, subject, sender, body string) Message {
	return Message{Mailbox: mailbox, UID: uid, Subject: subject, Sender: sender,
		Recipients: []string{"dest@example.com"}, Date: 1700000000, Snippet: snippet(body, 200), Body: body}
}

func TestIndexCRUDAndSearch(t *testing.T) {
	ix := testIndex(t)

	if err := ix.upsertMailbox("INBOX", 1); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 3)
	for i, m := range []Message{
		msg("INBOX", 1, "Quarterly report", "boss@corp.com", "the numbers look great this quarter"),
		msg("INBOX", 2, "Lunch plans", "alice@example.com", "sushi on friday?"),
		msg("Archive", 1, "Old report", "old@corp.com", "ancient quarterly numbers"),
	} {
		id, err := ix.insertMessage(m)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}

	// FTS search finds by subject and body.
	res, err := ix.search("quarterly", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(res), res)
	}

	// Scoped to a mailbox.
	res, err = ix.search("report", "Archive", 0, 0)
	if err != nil || len(res) != 1 || res[0]["mailbox"] != "Archive" {
		t.Fatalf("mailbox-scoped search: %v %v", res, err)
	}

	// Recent ordering and limit.
	res, _ = ix.recent("", 2)
	if len(res) != 2 {
		t.Fatalf("recent limit: %d", len(res))
	}

	// get returns the body.
	m, err := ix.get(ids[0])
	if err != nil || m.Body != "the numbers look great this quarter" || m.Sender != "boss@corp.com" {
		t.Fatalf("get: %v %#v", err, m)
	}

	// Flags update and lookup.
	if err := ix.updateFlags("INBOX", 1, []string{"\\Seen"}); err != nil {
		t.Fatal(err)
	}
	got, err := ix.lookup(ids[0])
	if err != nil || len(got.Flags) != 1 || got.Flags[0] != "\\Seen" {
		t.Fatalf("lookup flags: %v %#v", err, got)
	}

	// Deletion of expunged messages.
	if err := ix.deleteMessage("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	res, _ = ix.search("sushi", "", 0, 0)
	if len(res) != 0 {
		t.Fatalf("deleted message still searchable: %v", res)
	}

	// Mailbox listing with counts.
	mbs, err := ix.mailboxes()
	if err != nil || len(mbs) != 2 {
		t.Fatalf("mailboxes: %v %v", mbs, err)
	}

	// removeMailbox drops a mailbox and its messages.
	if err := ix.removeMailbox("Archive"); err != nil {
		t.Fatal(err)
	}
	if res, _ = ix.search("ancient", "", 0, 0); len(res) != 0 {
		t.Fatalf("removeMailbox left messages")
	}
	mbs, _ = ix.mailboxes()
	if len(mbs) != 1 {
		t.Fatalf("removeMailbox left the mailbox: %v", mbs)
	}
}

func TestUpsertConflict(t *testing.T) {
	ix := testIndex(t)
	m := msg("INBOX", 7, "one", "a@b.c", "kumquat smoothie recipe")
	if _, err := ix.insertMessage(m); err != nil {
		t.Fatal(err)
	}
	updated := m
	updated.Subject, updated.Body = "pproved later", "strawberry pie"
	updated.Snippet = "strawberry pie"
	if _, err := ix.insertMessage(updated); err != nil {
		t.Fatal(err)
	}
	res, err := ix.search("strawberry", "", 0, 0)
	if err != nil || len(res) != 1 {
		t.Fatalf("upsert failed: %v %v", res, err)
	}
	if res2, _ := ix.search("kumquat", "", 0, 0); len(res2) != 0 {
		t.Fatalf("old body still indexed after upsert: %v", res2)
	}
}

func TestExtractText(t *testing.T) {
	raw := "From: owner@example.com\r\nTo: owner@example.com\r\nSubject: S\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nwe should visit the zucchini festival\r\n"
	text, err := extractText(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if !strings.Contains(text, "zucchini festival") {
		t.Fatalf("plain text not extracted: %q", text)
	}

	// Multipart with HTML fallback.
	html := "From: a@b.c\r\nContent-Type: multipart/alternative; boundary=X\r\n\r\n--X\r\nContent-Type: text/html\r\n\r\n<p>kumquat <b>pie</b></p>\r\n--X--\r\n"
	text, err = extractText(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractText html: %v", err)
	}
	if !strings.Contains(text, "kumquat pie") {
		t.Fatalf("html text not extracted: %q", text)
	}
}
