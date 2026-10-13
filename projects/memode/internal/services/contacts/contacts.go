// Package contacts exposes iCloud Contacts (CardDAV) to scripts.
package contacts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/lib/go/dav"
	"codebase.bid/projects/memode/internal/config"
)

// Service is the CardDAV-backed contacts service.
type Service struct {
	cfg config.Config

	mu      sync.Mutex
	book    *dav.CardDAV
	derr    error
	lastTry time.Time
}

// New creates the contacts service.
func New(cfg config.Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) client(ctx context.Context) (*dav.CardDAV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.book != nil {
		return s.book, nil
	}
	if s.derr != nil && time.Since(s.lastTry) < time.Minute {
		return nil, s.derr
	}
	s.lastTry = time.Now()
	book, err := dav.DiscoverCardDAV(ctx, dav.Account{
		Endpoint: s.cfg.CardDAVEndpoint(),
		Username: s.cfg.ICloudUser,
		Password: s.cfg.ICloudPassword,
	})
	if err != nil {
		s.derr = fmt.Errorf("carddav discover: %w", err)
		return nil, s.derr
	}
	s.book, s.derr = book, nil
	return book, nil
}

// Setup verifies the account is reachable.
func (s *Service) Setup(ctx context.Context, ready func(bool)) {
	_, err := s.client(ctx)
	ready(err == nil)
}

// Register adds the Lua API.
func (s *Service) Register(reg *codemode.Registry) {
	reg.Register(codemode.Function{Namespace: "contacts", Name: "groups", Desc: "list address books ({path, name})", Fn: s.fnGroups})
	reg.Register(codemode.Function{Namespace: "contacts", Name: "list", Desc: "list all contacts. args: {group?}. returns contacts with path, uid, full_name, email, phone, org, note", Fn: s.fnList})
	reg.Register(codemode.Function{Namespace: "contacts", Name: "search", Desc: "substring search over name, email, phone and organization. args: {query, group?}", Fn: s.fnSearch})
	reg.Register(codemode.Function{Namespace: "contacts", Name: "get", Desc: "fetch one contact by path", Fn: s.fnGet})
	reg.Register(codemode.Function{Namespace: "contacts", Name: "upsert", Write: true, Desc: "create or update a contact. args: {group?, uid?, full_name, email?, phone?, org?, note?}. pass uid of an existing contact to update it", Fn: s.fnUpsert})
	reg.Register(codemode.Function{Namespace: "contacts", Name: "delete", Write: true, Desc: "delete the contact at path. args: {path}", Fn: s.fnDelete})
}

func (s *Service) fnGroups(ctx context.Context, _ map[string]any) (any, error) {
	book, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(book.Books))
	for _, b := range book.Books {
		out = append(out, map[string]any{"path": b.Path, "name": b.Name})
	}
	return out, nil
}

func (s *Service) contacts(ctx context.Context, args map[string]any) ([]dav.Contact, error) {
	book, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	group, _ := args["group"].(string)
	return book.Contacts(ctx, group)
}

func (s *Service) fnList(ctx context.Context, args map[string]any) (any, error) {
	return s.contacts(ctx, args)
}

func (s *Service) fnSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	all, err := s.contacts(ctx, args)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	var out []dav.Contact
	for _, c := range all {
		hay := strings.ToLower(c.FullName + " " + c.Email + " " + c.Phone + " " + c.Org)
		if strings.Contains(hay, needle) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Service) fnGet(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	book, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return book.GetContact(ctx, path)
}

func (s *Service) fnUpsert(ctx context.Context, args map[string]any) (any, error) {
	contact, ok := args["contact"].(map[string]any)
	if !ok {
		contact = args
	}
	fullName, _ := contact["full_name"].(string)
	if fullName == "" {
		return nil, fmt.Errorf("full_name is required")
	}
	book, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	group, _ := contact["group"].(string)
	ct := dav.Contact{FullName: fullName}
	ct.Email, _ = contact["email"].(string)
	ct.Phone, _ = contact["phone"].(string)
	ct.Org, _ = contact["org"].(string)
	ct.Note, _ = contact["note"].(string)
	if uid, ok := contact["uid"].(string); ok {
		ct.UID = uid
	}
	if ct.UID == "" {
		ct.UID = fmt.Sprintf("memode-%d", time.Now().UnixNano())
	}
	path, err := book.PutContact(ctx, group, ct)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "uid": ct.UID}, nil
}

func (s *Service) fnDelete(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	book, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	if err := book.DeleteContact(ctx, path); err != nil {
		return nil, err
	}
	return "deleted", nil
}
