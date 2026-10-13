package dav

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

// calMem is an in-memory caldav.Backend served over httptest.
type calMem struct {
	objects map[string]caldav.CalendarObject
}

func (b *calMem) CurrentUserPrincipal(context.Context) (string, error) { return "/me/", nil }
func (b *calMem) CalendarHomeSetPath(context.Context) (string, error)  { return "/me/calendars/", nil }
func (b *calMem) CreateCalendar(context.Context, *caldav.Calendar) error {
	return fmt.Errorf("not implemented")
}
func (b *calMem) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{{Path: "/me/calendars/home/", Name: "Home", SupportedComponentSet: []string{"VEVENT"}}}, nil
}
func (b *calMem) GetCalendar(_ context.Context, path string) (*caldav.Calendar, error) {
	if path != "/me/calendars/home/" {
		return nil, fmt.Errorf("not found")
	}
	cal := caldav.Calendar{Path: path}
	return &cal, nil
}
func (b *calMem) GetCalendarObject(_ context.Context, path string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	obj, ok := b.objects[path]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &obj, nil
}
func (b *calMem) ListCalendarObjects(_ context.Context, _ string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	var out []caldav.CalendarObject
	for _, o := range b.objects {
		out = append(out, o)
	}
	return out, nil
}
func (b *calMem) QueryCalendarObjects(_ context.Context, _ string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return b.ListCalendarObjects(context.Background(), "", nil)
}
func (b *calMem) PutCalendarObject(_ context.Context, path string, cal *ical.Calendar, _ *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	if b.objects == nil {
		b.objects = map[string]caldav.CalendarObject{}
	}
	obj := caldav.CalendarObject{Path: path, Data: cal}
	b.objects[path] = obj
	return &obj, nil
}
func (b *calMem) DeleteCalendarObject(_ context.Context, path string) error {
	if _, ok := b.objects[path]; !ok {
		return fmt.Errorf("not found")
	}
	delete(b.objects, path)
	return nil
}

func testCalDAV(t *testing.T) (*CalDAV, *httptest.Server, *calMem) {
	t.Helper()
	mem := &calMem{}
	ts := httptest.NewServer(&caldav.Handler{Backend: mem})
	t.Cleanup(ts.Close)
	cal, err := DiscoverCalDAV(context.Background(), Account{Endpoint: ts.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return cal, ts, mem
}

func TestCalDAVRoundTrip(t *testing.T) {
	cal, _, mem := testCalDAV(t)
	ctx := context.Background()

	if len(cal.Calendars) != 1 || cal.Principal != "/me/" || cal.HomeSet != "/me/calendars/" {
		t.Fatalf("discovery: %#v", cal)
	}

	// Create an event.
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	ev := Event{Summary: "Zucchini festival", Location: "Market square", Start: start, End: start.Add(2 * time.Hour)}
	path, err := cal.PutEvent(ctx, "", ev)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !strings.Contains(path, ".ics") {
		t.Fatalf("unexpected path %q", path)
	}

	// Read it back by range query and by path.
	events, err := cal.Events(ctx, "", start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Summary != "Zucchini festival" || !events[0].Start.Equal(start) {
		t.Fatalf("events: %#v", events)
	}
	got, err := cal.GetEvent(ctx, events[0].Path)
	if err != nil || got.Summary != "Zucchini festival" {
		t.Fatalf("get: %v %#v", err, got)
	}

	// Update: same UID overwrites.
	ev.UID = got.UID
	ev.Summary = "Zucchini festival (revised)"
	if _, err := cal.PutEvent(ctx, "", ev); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(mem.objects) != 1 {
		t.Fatalf("update should overwrite, have %d objects", len(mem.objects))
	}
	events, _ = cal.Events(ctx, "", start.Add(-time.Hour), start.Add(time.Hour))
	if len(events) != 1 || events[0].Summary != "Zucchini festival (revised)" {
		t.Fatalf("update not visible: %#v", events)
	}

	// Delete.
	if err := cal.DeleteEvent(ctx, path); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if events, _ = cal.Events(ctx, "", start.Add(-time.Hour), start.Add(time.Hour)); len(events) != 0 {
		t.Fatalf("deleted event still listed")
	}
}

// cardMem is an in-memory carddav.Backend served over httptest.
type cardMem struct {
	objects map[string]carddav.AddressObject
}

func (b *cardMem) CurrentUserPrincipal(context.Context) (string, error) { return "/me/", nil }
func (b *cardMem) AddressBookHomeSetPath(context.Context) (string, error) {
	return "/me/addressbooks/", nil
}
func (b *cardMem) CreateAddressBook(context.Context, *carddav.AddressBook) error {
	return fmt.Errorf("not implemented")
}
func (b *cardMem) ListAddressBooks(context.Context) ([]carddav.AddressBook, error) {
	return []carddav.AddressBook{{Path: "/me/addressbooks/contacts/", Name: "Contacts"}}, nil
}
func (b *cardMem) GetAddressBook(_ context.Context, path string) (*carddav.AddressBook, error) {
	ab := carddav.AddressBook{Path: path}
	return &ab, nil
}
func (b *cardMem) DeleteAddressBook(context.Context, string) error {
	return fmt.Errorf("not implemented")
}
func (b *cardMem) GetAddressObject(_ context.Context, path string, _ *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	obj, ok := b.objects[path]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &obj, nil
}
func (b *cardMem) ListAddressObjects(_ context.Context, _ string, _ *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	var out []carddav.AddressObject
	for _, o := range b.objects {
		out = append(out, o)
	}
	return out, nil
}
func (b *cardMem) QueryAddressObjects(_ context.Context, _ string, query *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	return b.ListAddressObjects(context.Background(), "", nil)
}
func (b *cardMem) PutAddressObject(_ context.Context, path string, card vcard.Card, _ *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	if b.objects == nil {
		b.objects = map[string]carddav.AddressObject{}
	}
	obj := carddav.AddressObject{Path: path, Card: card}
	b.objects[path] = obj
	return &obj, nil
}
func (b *cardMem) DeleteAddressObject(_ context.Context, path string) error {
	if _, ok := b.objects[path]; !ok {
		return fmt.Errorf("not found")
	}
	delete(b.objects, path)
	return nil
}

func TestCardDAVRoundTrip(t *testing.T) {
	mem := &cardMem{}
	ts := httptest.NewServer(&carddav.Handler{Backend: mem})
	t.Cleanup(ts.Close)
	book, err := DiscoverCardDAV(context.Background(), Account{Endpoint: ts.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	ctx := context.Background()
	if len(book.Books) != 1 || book.HomeSet != "/me/addressbooks/" {
		t.Fatalf("discovery: %#v", book)
	}

	// Create a contact.
	path, err := book.PutContact(ctx, "", Contact{FullName: "Alice Appleseed", Email: "alice@example.com", Phone: "+15551234"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	contacts, err := book.Contacts(ctx, "")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts: %v %#v", err, contacts)
	}
	if contacts[0].FullName != "Alice Appleseed" || contacts[0].Email != "alice@example.com" || contacts[0].Phone != "+15551234" {
		t.Fatalf("contact fields: %#v", contacts[0])
	}

	// Get by path.
	got, err := book.GetContact(ctx, contacts[0].Path)
	if err != nil || got.FullName != "Alice Appleseed" {
		t.Fatalf("get: %v %#v", err, got)
	}

	// Update overwrites.
	if _, err := book.PutContact(ctx, "", Contact{UID: contacts[0].UID, FullName: "Alice A. Appleseed"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(mem.objects) != 1 {
		t.Fatalf("update should overwrite")
	}

	// Delete.
	if err := book.DeleteContact(ctx, path); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if contacts, _ = book.Contacts(ctx, ""); len(contacts) != 0 {
		t.Fatalf("deleted contact still listed")
	}
}
