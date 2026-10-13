// Package calendar exposes iCloud Calendar (CalDAV) to scripts.
package calendar

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/lib/go/dav"
	"codebase.bid/projects/memode/internal/config"
)

// Service is the CalDAV-backed calendar service.
type Service struct {
	cfg config.Config

	mu      sync.Mutex
	cal     *dav.CalDAV
	derr    error
	lastTry time.Time
}

// New creates the calendar service.
func New(cfg config.Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) client(ctx context.Context) (*dav.CalDAV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cal != nil {
		return s.cal, nil
	}
	if s.derr != nil && time.Since(s.lastTry) < time.Minute {
		return nil, s.derr
	}
	s.lastTry = time.Now()
	cal, err := dav.DiscoverCalDAV(ctx, dav.Account{
		Endpoint: s.cfg.CalDAVEndpoint(),
		Username: s.cfg.ICloudUser,
		Password: s.cfg.ICloudPassword,
	})
	if err != nil {
		s.derr = fmt.Errorf("cal discover: %w", err)
		return nil, s.derr
	}
	s.cal, s.derr = cal, nil
	return cal, nil
}

// Setup verifies the account is reachable.
func (s *Service) Setup(ctx context.Context, ready func(bool)) {
	_, err := s.client(ctx)
	ready(err == nil)
}

// Register adds the Lua API.
func (s *Service) Register(reg *codemode.Registry) {
	reg.Register(codemode.Function{Namespace: "calendar", Name: "calendars", Desc: "list all calendars ({path, name})", Fn: s.fnCalendars})
	reg.Register(codemode.Function{Namespace: "calendar", Name: "events", Desc: "list events in a time range. args: {calendar?, from, to} (ISO 8601 times). returns events with path, uid, summary, description, location, start, end_time", Fn: s.fnEvents})
	reg.Register(codemode.Function{Namespace: "calendar", Name: "get", Desc: "fetch one event by path", Fn: s.fnGet})
	reg.Register(codemode.Function{Namespace: "calendar", Name: "upsert_event", Write: true, Desc: "create or update an event. args: {calendar?, uid?, summary, start, end_time, location?, description?, all_day?}. start and end_time are ISO 8601 times; pass uid of an existing event to update it. returns {path, uid}", Fn: s.fnUpsert})
	reg.Register(codemode.Function{Namespace: "calendar", Name: "delete_event", Write: true, Desc: "delete the event at path. args: {path}", Fn: s.fnDelete})
}

func (s *Service) fnCalendars(ctx context.Context, _ map[string]any) (any, error) {
	cal, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(cal.Calendars))
	for _, c := range cal.Calendars {
		out = append(out, map[string]any{"path": c.Path, "name": c.Name})
	}
	return out, nil
}

func (s *Service) fnEvents(ctx context.Context, args map[string]any) (any, error) {
	from, err := argTime(args, "from")
	if err != nil {
		return nil, err
	}
	to, err := argTime(args, "to")
	if err != nil {
		return nil, err
	}
	cal, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	calendarPath, _ := args["calendar"].(string)
	return cal.Events(ctx, calendarPath, from, to)
}

func (s *Service) fnGet(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	cal, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	return cal.GetEvent(ctx, path)
}

func (s *Service) fnUpsert(ctx context.Context, args map[string]any) (any, error) {
	event, ok := args["event"].(map[string]any)
	if !ok {
		// allow flat style: upsert_event({summary=..., start=...})
		event = args
	}
	summary, _ := event["summary"].(string)
	if summary == "" {
		return nil, fmt.Errorf("summary is required")
	}
	start, err := argTime(event, "start")
	if err != nil {
		return nil, err
	}
	// "end" is a Lua keyword, so scripts pass ["end"] or, preferably,
	// end_time; both are accepted here.
	var end time.Time
	if _, has := event["end_time"]; has {
		if end, err = argTime(event, "end_time"); err != nil {
			return nil, err
		}
	} else if _, has := event["end"]; has {
		if end, err = argTime(event, "end"); err != nil {
			return nil, err
		}
	}
	cal, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	calendarPath, _ := event["calendar"].(string)
	ev := dav.Event{
		Summary:     summary,
		Start:       start,
		End:         end,
		AllDay:      event["all_day"] == true,
		Description: strArg(event, "description"),
		Location:    strArg(event, "location"),
	}
	if uid, ok := event["uid"].(string); ok {
		ev.UID = uid
	}
	if ev.UID == "" {
		ev.UID = fmt.Sprintf("memode-%d@memode", time.Now().UnixNano())
	}
	path, err := cal.PutEvent(ctx, calendarPath, ev)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "uid": ev.UID}, nil
}

func (s *Service) fnDelete(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	cal, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	if err := cal.DeleteEvent(ctx, path); err != nil {
		return nil, err
	}
	return "deleted", nil
}

func strArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func argTime(args map[string]any, key string) (time.Time, error) {
	switch v := args[key].(type) {
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: parse %q as RFC3339: %w", key, v, err)
		}
		return t, nil
	case float64:
		return time.Unix(int64(v), 0), nil
	default:
		return time.Time{}, fmt.Errorf("%s: RFC3339 string or unix time required", key)
	}
}
