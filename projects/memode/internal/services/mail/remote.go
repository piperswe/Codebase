package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// remote is the live IMAP account. Every method opens its own connection so
// mutations never disturb the IDLE connection of the sync worker.
type remote struct {
	addr     string
	username string
	password string
	tlsConf  *tls.Config // non-nil only for tests with self-signed certs
}

func (r remote) dial(ctx context.Context) (*imapclient.Client, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second}, Config: r.tlsConf}
	conn, err := d.DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, fmt.Errorf("dial imap %s: %w", r.addr, err)
	}
	client := imapclient.New(conn, &imapclient.Options{})
	if err := client.Login(r.username, r.password).Wait(); err != nil {
		func() { _ = client.Logout().Wait() }()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return client, nil
}

// createMailbox creates a new mailbox.
func (r remote) createMailbox(ctx context.Context, name string) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()
	return client.Create(name, nil).Wait()
}

// mailboxes lists all mailbox names on the server.
func (r remote) mailboxes(ctx context.Context) ([]string, error) {
	client, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Logout().Wait() }()

	cmd := client.List("", "*", nil)
	defer cmd.Close()
	var out []string
	for {
		mb := cmd.Next()
		if mb == nil {
			break
		}
		if mb.Mailbox != "" {
			out = append(out, mb.Mailbox)
		}
	}
	return out, cmd.Close()
}

// syncMailbox brings the local index in sync with one server mailbox:
// vanished messages are removed, new UIDs are fetched in full, flags are
// refreshed for everything.
func (r remote) syncMailbox(ctx context.Context, ix index, mailbox string) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()

	data, err := client.Select(mailbox, nil).Wait()
	if err != nil {
		// The mailbox vanished server-side; drop it and its messages.
		return ix.removeMailbox(mailbox)
	}
	if err := ix.upsertMailbox(mailbox, data.UIDValidity); err != nil {
		return err
	}

	// One pass over all UIDs: refresh flags, collect new UIDs.
	var uids []uint32
	if data.NumMessages > 0 {
		all := imap.UIDSet{}
		all.AddRange(1, imap.UID(data.UIDNext-1))
		fetch := client.Fetch(all, &imap.FetchOptions{UID: true, Flags: true})
		var newUIDs []uint32
		for {
			msg := fetch.Next()
			if msg == nil {
				break
			}
			var flags []string
			var uid uint32
			for {
				item := msg.Next()
				if item == nil {
					break
				}
				switch v := item.(type) {
				case imapclient.FetchItemDataUID:
					uid = uint32(v.UID)
				case imapclient.FetchItemDataFlags:
					for _, f := range v.Flags {
						flags = append(flags, string(f))
					}
				}
			}
			if uid == 0 {
				continue
			}
			uids = append(uids, uid)
			if err := ix.updateFlags(mailbox, uid, flags); err != nil {
				return err
			}
			known, err := ix.hasMessage(mailbox, uid)
			if err != nil {
				return err
			}
			if !known {
				newUIDs = append(newUIDs, uid)
			}
		}
		if err := fetch.Close(); err != nil {
			return err
		}
		if err := r.fetchNew(ctx, client, ix, mailbox, newUIDs); err != nil {
			return err
		}
	}

	// Drop messages that no longer exist server-side.
	known := map[uint32]bool{}
	for _, uid := range uids {
		known[uid] = true
	}
	indexedUIDs, err := ix.uidsInMailbox(mailbox)
	if err != nil {
		return err
	}
	for _, uid := range indexedUIDs {
		if !known[uid] {
			if err := ix.deleteMessage(mailbox, uid); err != nil {
				return err
			}
		}
	}

	var lastUID uint32
	if len(uids) > 0 {
		lastUID = uids[len(uids)-1]
	}
	return ix.setSynced(mailbox, lastUID)
}

// fetchNew downloads and indexes messages not yet present locally.
func (r remote) fetchNew(_ context.Context, client *imapclient.Client, ix index, mailbox string, newUIDs []uint32) error {
	if len(newUIDs) == 0 {
		return nil
	}
	set := imap.UIDSet{}
	for _, uid := range newUIDs {
		set.AddNum(imap.UID(uid))
	}
	fetch := client.Fetch(set, &imap.FetchOptions{
		UID: true, Envelope: true, InternalDate: true, RFC822Size: true,
		BodySection: []*imap.FetchItemBodySection{{Specifier: "", Peek: true}},
	})
	for {
		msg := fetch.Next()
		if msg == nil {
			break
		}
		m := Message{Mailbox: mailbox}
		hasBody := false
		for {
			item := msg.Next()
			if item == nil {
				break
			}
			switch v := item.(type) {
			case imapclient.FetchItemDataUID:
				m.UID = uint32(v.UID)
			case imapclient.FetchItemDataEnvelope:
				if v.Envelope != nil {
					m.Subject = v.Envelope.Subject
					if len(v.Envelope.From) > 0 {
						m.Sender = v.Envelope.From[0].Addr()
					}
					for _, to := range v.Envelope.To {
						if addr := to.Addr(); addr != "" {
							m.Recipients = append(m.Recipients, addr)
						}
					}
					if v.Envelope.MessageID != "" {
						m.MessageID = strings.Trim(v.Envelope.MessageID, "<>")
					}
				}
			case imapclient.FetchItemDataInternalDate:
				m.Date = v.Time.Unix()
			case imapclient.FetchItemDataRFC822Size:
				m.Size = v.Size
			case imapclient.FetchItemDataBodySection:
				// The literal must be read now, before the next item is
				// consumed.
				m.Body, _ = extractText(v.Literal)
				hasBody = true
			}
		}
		if m.UID == 0 || !hasBody {
			continue
		}
		m.Snippet = snippet(m.Body, 200)
		if _, err := ix.insertMessage(m); err != nil {
			return err
		}
	}
	return fetch.Close()
}

// uidsInMailbox lists all indexed UIDs of a mailbox.
func (ix index) uidsInMailbox(mailbox string) ([]uint32, error) {
	rows, err := ix.db.Query(`SELECT uid FROM mail_messages WHERE mailbox = ?`, mailbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

// setFlags adds or removes flags on one message.
func (r remote) setFlags(ctx context.Context, mailbox string, uid uint32, op imap.StoreFlagsOp, flags []string) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		return err
	}
	set := imap.UIDSet{}
	set.AddNum(imap.UID(uid))
	return client.Store(set, &imap.StoreFlags{Op: op, Flags: toIMAPFlags(flags)}, nil).Close()
}

// move moves one message to another mailbox.
func (r remote) move(ctx context.Context, mailbox string, uid uint32, dest string) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		return err
	}
	set := imap.UIDSet{}
	set.AddNum(imap.UID(uid))
	_, err = client.Move(set, dest).Wait()
	return err
}

// delete marks one message \Deleted and expunges it.
func (r remote) delete(ctx context.Context, mailbox string, uid uint32) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		return err
	}
	set := imap.UIDSet{}
	set.AddNum(imap.UID(uid))
	if err := client.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return err
	}
	return client.Expunge().Close()
}

// idleBlocks waits for changes in one mailbox until ctx is done or the
// connection breaks, then returns.
func (r remote) idleBlocks(ctx context.Context, mailbox string) error {
	client, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Logout().Wait() }()
	if _, err := client.Select(mailbox, nil).Wait(); err != nil {
		return err
	}
	idle, err := client.Idle()
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = idle.Close()
	}()
	// Wake when the connection dies (e.g. server-side IDLE timeout) so the
	// caller can reconnect.
	closed := make(chan struct{})
	go func() {
		<-client.Closed()
		close(closed)
	}()
	select {
	case <-ctx.Done():
		_ = idle.Close()
		return idle.Wait()
	case <-closed:
		_ = idle.Close()
		_ = idle.Wait()
		return fmt.Errorf("idle connection lost")
	}
}

var (
	htmlTag = regexp.MustCompile(`<[^>]*>`)
	wsRun   = regexp.MustCompile(`\s+`)
)

// extractText pulls the plain text out of a MIME message, falling back to
// tag-stripped HTML. shortcut: crude HTML stripping, fine for search indexing.
func extractText(r io.Reader) (string, error) {
	reader, err := mail.CreateReader(r)
	if err != nil && !isUnknownCharset(err) {
		return "", err
	}
	var plain, html string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !isUnknownCharset(err) {
			return "", err
		}
		if part == nil {
			break
		}
		ct := part.Header.Get("Content-Type")
		if i := strings.Index(ct, ";"); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
		body, err := io.ReadAll(io.LimitReader(part.Body, 1<<20))
		if err != nil {
			continue
		}
		switch {
		case ct == "text/plain" && plain == "":
			plain = string(body)
		case strings.HasPrefix(ct, "text/html") && html == "":
			html = string(body)
		}
	}
	if plain != "" {
		return plain, nil
	}
	if html != "" {
		return strings.TrimSpace(wsRun.ReplaceAllString(htmlTag.ReplaceAllString(html, " "), " ")), nil
	}
	return "", nil
}

// isUnknownCharset wraps go-message's sentinel for unknown charsets.
func isUnknownCharset(err error) bool { return message.IsUnknownCharset(err) }

func snippet(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > n {
		runes = runes[:n]
	}
	return string(runes)
}

// toIMAPFlags converts plain strings to imap.Flag.
func toIMAPFlags(flags []string) []imap.Flag {
	out := make([]imap.Flag, len(flags))
	for i, f := range flags {
		out[i] = imap.Flag(f)
	}
	return out
}
