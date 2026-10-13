package mail

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// write.go executes mutations. Every mutation is applied upstream first, then
// the affected mailbox is re-synced synchronously, so the next read in the
// same script already sees the change.

func (s *Service) fnSend(ctx context.Context, args map[string]any) (any, error) {
	to, err := argAddrList(args["to"])
	if err != nil {
		return nil, err
	}
	if len(to) == 0 {
		return nil, fmt.Errorf("to is required")
	}
	cc, err := argAddrList(args["cc"])
	if err != nil {
		return nil, err
	}
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	if subject == "" && body == "" {
		return nil, fmt.Errorf("refusing to send an empty email")
	}

	// A minimal MIME message: headers plus one text/plain part.
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\r\n", s.cfg.ICloudUser)
	fmt.Fprintf(&sb, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&sb, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&sb, "Subject: %s\r\n", subject)
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	sb.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))

	auth := smtp.PlainAuth("", s.cfg.ICloudUser, s.cfg.ICloudPassword, s.cfg.SMTPHost())
	if err := smtp.SendMail(s.cfg.SMTPAddr(), auth, s.cfg.ICloudUser, append(to, cc...), []byte(sb.String())); err != nil {
		return nil, fmt.Errorf("smtp send: %w", err)
	}

	// Pull the sent copy (and anything new in INBOX) into the index.
	s.sync.resyncNow(ctx, "INBOX")
	s.resyncSentMailboxes(ctx)
	return "sent", nil
}

func (s *Service) resyncSentMailboxes(ctx context.Context) {
	names, err := s.remote.mailboxes(ctx)
	if err != nil {
		return
	}
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "sent") {
			s.sync.resyncNow(ctx, n)
		}
	}
}

func argAddrList(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		if t == "" {
			return nil, nil
		}
		return []string{t}, nil
	case []any:
		var out []string
		for _, item := range t {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected address string or list")
	}
}

func (s *Service) fnSetFlags(ctx context.Context, args map[string]any) (any, error) {
	id, ok := argID(args)
	if !ok {
		return nil, fmt.Errorf("id is required")
	}
	msg, err := s.index.lookup(id)
	if err != nil {
		return nil, err
	}
	for _, flag := range []struct {
		key  string
		name string
	}{
		{"seen", "\\Seen"},
		{"flagged", "\\Flagged"},
	} {
		value, ok := args[flag.key].(bool)
		if !ok {
			continue
		}
		op := imap.StoreFlagsDel
		if value {
			op = imap.StoreFlagsAdd
		}
		if err := s.remote.setFlags(ctx, msg.Mailbox, msg.UID, op, []string{flag.name}); err != nil {
			return nil, err
		}
	}
	s.sync.resyncNow(ctx, msg.Mailbox)
	return "ok", nil
}

func (s *Service) fnMove(ctx context.Context, args map[string]any) (any, error) {
	id, ok := argID(args)
	if !ok {
		return nil, fmt.Errorf("id is required")
	}
	dest, _ := args["to"].(string)
	if dest == "" {
		return nil, fmt.Errorf("to is required")
	}
	msg, err := s.index.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := s.remote.move(ctx, msg.Mailbox, msg.UID, dest); err != nil {
		return nil, err
	}
	s.sync.resyncNow(ctx, msg.Mailbox, dest)
	return "moved", nil
}

func (s *Service) fnDelete(ctx context.Context, args map[string]any) (any, error) {
	id, ok := argID(args)
	if !ok {
		return nil, fmt.Errorf("id is required")
	}
	msg, err := s.index.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := s.remote.delete(ctx, msg.Mailbox, msg.UID); err != nil {
		return nil, err
	}
	s.sync.resyncNow(ctx, msg.Mailbox)
	return "deleted", nil
}
