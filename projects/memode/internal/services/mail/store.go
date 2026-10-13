// Package mail keeps a local full-text index of the owner's IMAP mailboxes,
// continuously synced, and executes reads against the index and writes
// directly against IMAP/SMTP followed by a synchronous re-sync.
package mail

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Message is one indexed email.
type Message struct {
	ID         int64    `json:"id"`
	Mailbox    string   `json:"mailbox"`
	UID        uint32   `json:"uid"`
	MessageID  string   `json:"message_id,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Sender     string   `json:"sender,omitempty"`
	Recipients []string `json:"recipients,omitempty"`
	Date       int64    `json:"date,omitempty"` // unix seconds
	Snippet    string   `json:"snippet,omitempty"`
	Body       string   `json:"body,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	Size       int64    `json:"size,omitempty"`
}

// index is the SQLite-backed local mail index.
type index struct{ db *sql.DB }

// upsertMailbox records a mailbox and its UIDVALIDITY.
func (ix index) upsertMailbox(name string, uidvalidity uint32) error {
	_, err := ix.db.Exec(`INSERT INTO mailboxes (name, uidvalidity) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET uidvalidity = excluded.uidvalidity`, name, uidvalidity)
	return err
}

// resetMailbox removes all messages of a mailbox whose UIDVALIDITY changed.
func (ix index) resetMailbox(name string) error {
	_, err := ix.db.Exec(`DELETE FROM mail_messages WHERE mailbox = ?`, name)
	return err
}

// setSynced stamps the mailbox sync state.
func (ix index) setSynced(name string, lastUID uint32) error {
	_, err := ix.db.Exec(`UPDATE mailboxes SET last_uid = ?, synced_at = ? WHERE name = ?`, lastUID, time.Now().Unix(), name)
	return err
}

// removeMailbox drops a vanished mailbox and its messages.
func (ix index) removeMailbox(name string) error {
	if _, err := ix.db.Exec(`DELETE FROM mail_messages WHERE mailbox = ?`, name); err != nil {
		return err
	}
	_, err := ix.db.Exec(`DELETE FROM mailboxes WHERE name = ?`, name)
	return err
}

// hasMessage reports whether (mailbox, uid) is already indexed.
func (ix index) hasMessage(mailbox string, uid uint32) (bool, error) {
	var one int
	err := ix.db.QueryRow(`SELECT 1 FROM mail_messages WHERE mailbox = ? AND uid = ?`, mailbox, uid).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// insertMessage indexes a fetched message.
func (ix index) insertMessage(m Message) (int64, error) {
	if err := ix.upsertMailbox(m.Mailbox, 0); err != nil {
		return 0, err
	}
	recipients, err := json.Marshal(m.Recipients)
	if err != nil {
		return 0, err
	}
	flags, err := json.Marshal(m.Flags)
	if err != nil {
		return 0, err
	}
	if m.Recipients == nil {
		recipients = []byte("null")
	}
	if m.Flags == nil {
		flags = []byte("null")
	}
	res, err := ix.db.Exec(`INSERT INTO mail_messages
		(mailbox, uid, message_id, subject, sender, recipients, date, snippet, body, flags, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mailbox, uid) DO UPDATE SET
		  message_id = excluded.message_id, subject = excluded.subject, sender = excluded.sender,
		  recipients = excluded.recipients, date = excluded.date, snippet = excluded.snippet,
		  body = excluded.body, size = excluded.size`,
		m.Mailbox, m.UID, m.MessageID, m.Subject, m.Sender, string(recipients), m.Date, m.Snippet, m.Body, string(flags), m.Size)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// updateFlags refreshes the flags of one indexed message.
func (ix index) updateFlags(mailbox string, uid uint32, flags []string) error {
	b, err := json.Marshal(flags)
	if err != nil {
		return err
	}
	_, err = ix.db.Exec(`UPDATE mail_messages SET flags = ? WHERE mailbox = ? AND uid = ?`, string(b), mailbox, uid)
	return err
}

// deleteMessage removes an expunged message.
func (ix index) deleteMessage(mailbox string, uid uint32) error {
	_, err := ix.db.Exec(`DELETE FROM mail_messages WHERE mailbox = ? AND uid = ?`, mailbox, uid)
	return err
}

// lookup resolves a message id to its current mailbox and UID.
func (ix index) lookup(id int64) (*Message, error) {
	row := ix.db.QueryRow(`SELECT id, mailbox, uid, subject, sender, recipients, flags FROM mail_messages WHERE id = ?`, id)
	var m Message
	var recipients, flags string
	if err := row.Scan(&m.ID, &m.Mailbox, &m.UID, &m.Subject, &m.Sender, &recipients, &flags); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no message with id %d", id)
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(recipients), &m.Recipients)
	_ = json.Unmarshal([]byte(flags), &m.Flags)
	return &m, nil
}

// summary is a message without its body.
type summary = map[string]any

func (ix index) search(query, mailbox string, limit, offset int) ([]summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sqlStr := `SELECT m.id, m.mailbox, m.subject, m.sender, m.date, m.snippet, m.flags
		FROM mail_fts f JOIN mail_messages m ON m.id = f.rowid
		WHERE mail_fts MATCH ?`
	args := []any{query}
	if mailbox != "" {
		sqlStr += ` AND m.mailbox = ?`
		args = append(args, mailbox)
	}
	sqlStr += ` ORDER BY m.date DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return ix.rowsToSummaries(sqlStr, args...)
}

func (ix index) recent(mailbox string, limit int) ([]summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sqlStr := `SELECT id, mailbox, subject, sender, date, snippet, flags FROM mail_messages`
	args := []any{}
	if mailbox != "" {
		sqlStr += ` WHERE mailbox = ?`
		args = append(args, mailbox)
	}
	sqlStr += ` ORDER BY date DESC LIMIT ?`
	args = append(args, limit)
	return ix.rowsToSummaries(sqlStr, args...)
}

func (ix index) rowsToSummaries(sqlStr string, args ...any) ([]summary, error) {
	rows, err := ix.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []summary
	for rows.Next() {
		var (
			id, date                                 int64
			mailbox, subject, sender, snippet, flags string
		)
		if err := rows.Scan(&id, &mailbox, &subject, &sender, &date, &snippet, &flags); err != nil {
			return nil, err
		}
		var flagList []string
		_ = json.Unmarshal([]byte(flags), &flagList)
		out = append(out, summary{
			"id": id, "mailbox": mailbox, "subject": subject, "sender": sender,
			"date": date, "snippet": snippet, "flags": flagList,
		})
	}
	return out, rows.Err()
}

func (ix index) get(id int64) (*Message, error) {
	row := ix.db.QueryRow(`SELECT id, mailbox, uid, message_id, subject, sender, recipients, date, snippet, body, flags, size
		FROM mail_messages WHERE id = ?`, id)
	var m Message
	var recipients, flags string
	if err := row.Scan(&m.ID, &m.Mailbox, &m.UID, &m.MessageID, &m.Subject, &m.Sender, &recipients, &m.Date, &m.Snippet, &m.Body, &flags, &m.Size); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no message with id %d", id)
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(recipients), &m.Recipients)
	_ = json.Unmarshal([]byte(flags), &m.Flags)
	return &m, nil
}

// mailboxes lists indexed mailboxes with message counts.
func (ix index) mailboxes() ([]summary, error) {
	rows, err := ix.db.Query(`SELECT name, (SELECT count(*) FROM mail_messages m WHERE m.mailbox = mailboxes.name)
		FROM mailboxes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []summary
	for rows.Next() {
		var name string
		var count int64
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out = append(out, summary{"name": name, "count": count})
	}
	return out, rows.Err()
}

// names returns the indexed mailbox names.
func (ix index) names() ([]string, error) {
	rows, err := ix.db.Query(`SELECT name FROM mailboxes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
