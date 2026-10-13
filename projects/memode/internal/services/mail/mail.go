package mail

import (
	"context"
	"database/sql"
	"fmt"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
)

// Service is the iCloud Mail service: local full-text index, continuous IMAP
// sync, writes straight to IMAP/SMTP with synchronous post-write re-sync.
type Service struct {
	cfg    config.Config
	db     *sql.DB
	remote remote
	index  index
	sync   *syncer
}

// New creates the mail service.
func New(cfg config.Config, db *sql.DB) *Service {
	s := &Service{
		cfg: cfg,
		db:  db,
		remote: remote{
			addr:     cfg.IMAPAddr(),
			username: cfg.ICloudUser,
			password: cfg.ICloudPassword,
			tlsConf:  cfg.IMAPInsecureTLS(),
		},
		index: index{db: db},
	}
	s.sync = newSyncer(s.remote, s.index)
	return s
}

// Setup starts the background sync worker and verifies the IMAP credentials in
// the background, so server startup never blocks on a slow or unreachable
// mail server.
func (s *Service) Setup(ctx context.Context, ready func(bool)) {
	go s.sync.run(ctx)
	go func() {
		if _, err := s.remote.mailboxes(ctx); err != nil {
			ready(false)
			return
		}
		ready(true)
	}()
}

// Register adds the Lua API.
func (s *Service) Register(reg *codemode.Registry) {
	ns := "mail"
	regFn := reg.Register

	regFn(codemode.Function{Namespace: ns, Name: "mailboxes", Desc: "list all mailboxes with message counts", Fn: s.fnMailboxes})
	regFn(codemode.Function{Namespace: ns, Name: "search", Desc: "full-text search. args: {query (FTS5 syntax), mailbox?, limit?, offset?}. returns message summaries: id, mailbox, subject, sender, date, snippet, flags", Fn: s.fnSearch})
	regFn(codemode.Function{Namespace: ns, Name: "recent", Desc: "newest messages. args: {mailbox?, limit?}. returns message summaries like mail.search", Fn: s.fnRecent})
	regFn(codemode.Function{Namespace: ns, Name: "get", Desc: "fetch one full message by id (as returned by search/recent), including body text", Fn: s.fnGet})
	regFn(codemode.Function{Namespace: ns, Name: "send", Write: true, Desc: "send an email. args: {to (string or list), cc?, subject, body}. returns ok", Fn: s.fnSend})
	regFn(codemode.Function{Namespace: ns, Name: "set_flags", Write: true, Desc: "set flags on a message. args: {id, seen? (bool), flagged? (bool)}. nil fields are left unchanged", Fn: s.fnSetFlags})
	regFn(codemode.Function{Namespace: ns, Name: "move", Write: true, Desc: "move a message to another mailbox. args: {id, to}", Fn: s.fnMove})
	regFn(codemode.Function{Namespace: ns, Name: "delete", Write: true, Desc: "permanently delete a message. args: {id}", Fn: s.fnDelete})
	regFn(codemode.Function{Namespace: ns, Name: "sync", Write: true, Desc: "synchronously re-sync mailboxes now (the worker does this continuously anyway). args: {mailboxes? (list)}", Fn: s.fnSync})
}

func (s *Service) fnMailboxes(_ context.Context, _ map[string]any) (any, error) {
	return s.index.mailboxes()
}

func (s *Service) fnSearch(_ context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	mailbox, _ := args["mailbox"].(string)
	limit, offset := argInt(args, "limit", 0), argInt(args, "offset", 0)
	return s.index.search(query, mailbox, limit, offset)
}

func (s *Service) fnRecent(_ context.Context, args map[string]any) (any, error) {
	mailbox, _ := args["mailbox"].(string)
	return s.index.recent(mailbox, argInt(args, "limit", 0))
}

func (s *Service) fnGet(_ context.Context, args map[string]any) (any, error) {
	id, ok := argID(args)
	if !ok {
		return nil, fmt.Errorf("id is required")
	}
	return s.index.get(id)
}

func (s *Service) fnSync(ctx context.Context, args map[string]any) (any, error) {
	var mailboxes []string
	if list, ok := args["mailboxes"].([]any); ok {
		for _, v := range list {
			if name, ok := v.(string); ok {
				mailboxes = append(mailboxes, name)
			}
		}
	}
	s.sync.resyncNow(ctx, mailboxes...)
	return "synced", nil
}

func argInt(args map[string]any, key string, def int64) int {
	if n, ok := args[key].(float64); ok {
		return int(n)
	}
	return int(def)
}

func argID(args map[string]any) (int64, bool) {
	if n, ok := args["id"].(float64); ok {
		return int64(n), true
	}
	return 0, false
}
