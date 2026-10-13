// Package davtest serves in-memory CalDAV and CardDAV servers built on
// go-webdav's server implementations, for tests that need a real DAV endpoint
// without a container.
//
// It is a normal (non-test) package only because Go cannot share test files
// across packages; nothing in production code imports it.
package davtest

import (
	"context"
	"fmt"
	"net/http/httptest"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

// Paths follow the server's depth convention: the principal sits at /me/ and
// home sets one level below, e.g. /me/calendars/.

// CalServer is an in-memory CalDAV server with one VEVENT calendar.
func CalServer() *httptest.Server {
	return httptest.NewServer(&caldav.Handler{Backend: &calMem{}})
}

// CardServer is an in-memory CardDAV server with one address book.
func CardServer() *httptest.Server {
	return httptest.NewServer(&carddav.Handler{Backend: &cardMem{}})
}

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
	return &caldav.Calendar{Path: path}, nil
}
func (b *calMem) GetCalendarObject(_ context.Context, path string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	obj, ok := b.objects[path]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &obj, nil
}
func (b *calMem) ListCalendarObjects(context.Context, string, *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	var out []caldav.CalendarObject
	for _, o := range b.objects {
		out = append(out, o)
	}
	return out, nil
}
func (b *calMem) QueryCalendarObjects(ctx context.Context, _ string, _ *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return b.ListCalendarObjects(ctx, "", nil)
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
	return &carddav.AddressBook{Path: path}, nil
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
func (b *cardMem) ListAddressObjects(context.Context, string, *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	var out []carddav.AddressObject
	for _, o := range b.objects {
		out = append(out, o)
	}
	return out, nil
}
func (b *cardMem) QueryAddressObjects(ctx context.Context, _ string, _ *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	return b.ListAddressObjects(ctx, "", nil)
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
