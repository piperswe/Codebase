// Package dav wraps emersion/go-webdav's CalDAV and CardDAV clients with
// account discovery and typed event/contact helpers, so callers do not have
// to deal with principal URLs, home sets or raw iCalendar/vCard data.
package dav

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

// Account is one DAV server login. Endpoint is the service root, e.g.
// https://caldav.icloud.com/ or https://carddav.icloud.com/.
type Account struct {
	Endpoint string
	Username string
	Password string
	// HTTP, nil means http.DefaultClient.
	HTTP webdav.HTTPClient
}

func (a Account) client() webdav.HTTPClient {
	if a.HTTP == nil {
		return http.DefaultClient
	}
	return a.HTTP
}

// Event is one calendar event.
type Event struct {
	Path        string    `json:"path,omitempty"`
	UID         string    `json:"uid,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end_time,omitempty"` // not "end": reserved word in Lua
	AllDay      bool      `json:"all_day,omitempty"`
}

// CalDAV is a discovered calendar account.
type CalDAV struct {
	account   Account
	client    *caldav.Client
	Principal string
	HomeSet   string
	Calendars []caldav.Calendar
}

// DiscoverCalDAV resolves the current user's principal, calendar home set and
// calendar list.
func DiscoverCalDAV(ctx context.Context, a Account) (*CalDAV, error) {
	httpClient := webdav.HTTPClientWithBasicAuth(a.HTTP, a.Username, a.Password)
	calClient, err := caldav.NewClient(httpClient, a.Endpoint)
	if err != nil {
		return nil, err
	}
	webdavClient, err := webdav.NewClient(httpClient, a.Endpoint)
	if err != nil {
		return nil, err
	}
	principal, err := webdavClient.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("find principal: %w", err)
	}
	home, err := calClient.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("find calendar home set: %w", err)
	}
	cals, err := calClient.FindCalendars(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("find calendars: %w", err)
	}
	return &CalDAV{account: a, client: calClient, Principal: principal, HomeSet: home, Calendars: cals}, nil
}

// Events lists events of one calendar that overlap [start, end). calendarPath
// defaults to the first calendar supporting VEVENT.
func (c *CalDAV) Events(ctx context.Context, calendarPath string, start, end time.Time) ([]Event, error) {
	path, err := c.resolveCalendar(calendarPath)
	if err != nil {
		return nil, err
	}
	objs, err := c.client.QueryCalendar(ctx, path, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, Comps: []caldav.CalendarCompRequest{{Name: "VEVENT", AllProps: true}}},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: end}}},
	})
	if err != nil {
		return nil, fmt.Errorf("query calendar: %w", err)
	}
	var out []Event
	for _, obj := range objs {
		out = append(out, calendarObjectEvents(obj)...)
	}
	return out, nil
}

// GetEvent fetches one event by path.
func (c *CalDAV) GetEvent(ctx context.Context, path string) (*Event, error) {
	obj, err := c.client.GetCalendarObject(ctx, path)
	if err != nil {
		return nil, err
	}
	events := calendarObjectEvents(*obj)
	if len(events) == 0 {
		return nil, fmt.Errorf("no VEVENT at %s", path)
	}
	return &events[0], nil
}

// PutEvent creates or overwrites the event identified by its UID inside the
// given calendar. calendarPath defaults like Events.
func (c *CalDAV) PutEvent(ctx context.Context, calendarPath string, e Event) (string, error) {
	path, err := c.resolveCalendar(calendarPath)
	if err != nil {
		return "", err
	}
	if e.UID == "" {
		e.UID = fmt.Sprintf("memode-%d@memode", time.Now().UnixNano())
	}
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, "-//codebase.bid//memode//EN")
	cal.Props.SetText(ical.PropVersion, "2.0")
	ev := ical.NewEvent()
	ev.Props.SetText("UID", e.UID)
	ev.Props.SetText(ical.PropSummary, e.Summary)
	if e.Location != "" {
		ev.Props.SetText(ical.PropLocation, e.Location)
	}
	if e.Description != "" {
		ev.Props.SetText(ical.PropDescription, e.Description)
	}
	if e.AllDay {
		ev.Props.SetDate("DTSTART", e.Start)
		if !e.End.IsZero() {
			ev.Props.SetDate("DTEND", e.End)
		}
	} else {
		start := ical.NewProp("DTSTART")
		start.SetDateTime(e.Start)
		ev.Props.Set(start)
		if !e.End.IsZero() {
			end := ical.NewProp("DTEND")
			end.SetDateTime(e.End)
			ev.Props.Set(end)
		}
	}
	ev.Props.SetText("DTSTAMP", time.Now().UTC().Format("20060102T150405Z"))
	cal.Children = append(cal.Children, ev.Component)

	objPath := strings.TrimSuffix(path, "/") + "/" + e.UID + ".ics"
	obj, err := c.client.PutCalendarObject(ctx, objPath, cal)
	if err != nil {
		return "", fmt.Errorf("put event: %w", err)
	}
	if obj != nil {
		return obj.Path, nil
	}
	return objPath, nil
}

// DeleteEvent deletes the event at path.
func (c *CalDAV) DeleteEvent(ctx context.Context, path string) error {
	return c.delete(ctx, path)
}

func (c *CalDAV) delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, resolveURL(c.account.Endpoint, path), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.account.Username, c.account.Password)
	resp, err := c.account.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("DELETE %s: %s", path, resp.Status)
	}
	return nil
}

func (c *CalDAV) resolveCalendar(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	for _, cal := range c.Calendars {
		for _, comp := range cal.SupportedComponentSet {
			if comp == "VEVENT" {
				return cal.Path, nil
			}
		}
	}
	return "", fmt.Errorf("no calendar found")
}

// calendarObjectEvents converts a raw object to plain Events.
func calendarObjectEvents(obj caldav.CalendarObject) []Event {
	var out []Event
	if obj.Data == nil {
		return out
	}
	for _, comp := range obj.Data.Children {
		if comp.Name != "VEVENT" {
			continue
		}
		e := Event{Path: obj.Path}
		e.UID = comp.Props.Get("UID").Value
		if p := comp.Props.Get(ical.PropSummary); p != nil {
			e.Summary = p.Value
		}
		if p := comp.Props.Get(ical.PropLocation); p != nil {
			e.Location = p.Value
		}
		if p := comp.Props.Get(ical.PropDescription); p != nil {
			e.Description = p.Value
		}
		if p := comp.Props.Get("DTSTART"); p != nil {
			start, err := p.DateTime(time.UTC)
			if err == nil {
				e.Start = start
				e.AllDay = p.ValueType() == ical.ValueDate
			}
		}
		if p := comp.Props.Get("DTEND"); p != nil {
			if end, err := p.DateTime(time.UTC); err == nil {
				e.End = end
			}
		}
		out = append(out, e)
	}
	return out
}

// resolveURL joins the endpoint host with a server-absolute path.
func resolveURL(endpoint, path string) string {
	base := strings.TrimRight(endpoint, "/")
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// Contact is one address-book entry.
type Contact struct {
	Path     string `json:"path,omitempty"`
	UID      string `json:"uid,omitempty"`
	FullName string `json:"full_name,omitempty"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Org      string `json:"org,omitempty"`
	Note     string `json:"note,omitempty"`
}

// CardDAV is a discovered address-book account.
type CardDAV struct {
	account   Account
	client    *carddav.Client
	Principal string
	HomeSet   string
	Books     []carddav.AddressBook
}

// DiscoverCardDAV resolves the current user's principal, address-book home
// set and address-book list.
func DiscoverCardDAV(ctx context.Context, a Account) (*CardDAV, error) {
	httpClient := webdav.HTTPClientWithBasicAuth(a.HTTP, a.Username, a.Password)
	cardClient, err := carddav.NewClient(httpClient, a.Endpoint)
	if err != nil {
		return nil, err
	}
	webdavClient, err := webdav.NewClient(httpClient, a.Endpoint)
	if err != nil {
		return nil, err
	}
	principal, err := webdavClient.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("find principal: %w", err)
	}
	home, err := cardClient.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("find address book home set: %w", err)
	}
	books, err := cardClient.FindAddressBooks(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("find address books: %w", err)
	}
	return &CardDAV{account: a, client: cardClient, Principal: principal, HomeSet: home, Books: books}, nil
}

// Contacts lists all contacts in one address book. bookPath defaults to the
// first address book.
func (c *CardDAV) Contacts(ctx context.Context, bookPath string) ([]Contact, error) {
	path, err := c.resolveBook(bookPath)
	if err != nil {
		return nil, err
	}
	objs, err := c.client.QueryAddressBook(ctx, path, &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{AllProp: true},
	})
	if err != nil {
		return nil, fmt.Errorf("query address book: %w", err)
	}
	var out []Contact
	for _, obj := range objs {
		out = append(out, addressObjectContact(obj))
	}
	return out, nil
}

// GetContact fetches one contact by path.
func (c *CardDAV) GetContact(ctx context.Context, path string) (*Contact, error) {
	obj, err := c.client.GetAddressObject(ctx, path)
	if err != nil {
		return nil, err
	}
	contact := addressObjectContact(*obj)
	return &contact, nil
}

// PutContact creates or overwrites the contact identified by its UID inside
// the given address book.
func (c *CardDAV) PutContact(ctx context.Context, bookPath string, ct Contact) (string, error) {
	path, err := c.resolveBook(bookPath)
	if err != nil {
		return "", err
	}
	if ct.UID == "" {
		ct.UID = fmt.Sprintf("memode-%d", time.Now().UnixNano())
	}
	card := make(vcard.Card)
	if ct.FullName != "" {
		card.SetValue("FN", ct.FullName)
	}
	// N is required by RFC 6350; derive from FN when unspecified.
	n := ct.FullName
	card.SetValue("N", strings.Join([]string{"", "", n, "", ""}, ";"))
	card.SetValue(vcard.FieldUID, ct.UID)
	card.SetValue(vcard.FieldVersion, "4.0")
	if ct.Email != "" {
		card.Add(vcard.FieldEmail, &vcard.Field{Value: ct.Email})
	}
	if ct.Phone != "" {
		card.Add(vcard.FieldTelephone, &vcard.Field{Value: ct.Phone})
	}
	if ct.Org != "" {
		card.Add(vcard.FieldOrganization, &vcard.Field{Value: ct.Org})
	}
	if ct.Note != "" {
		card.Add(vcard.FieldNote, &vcard.Field{Value: ct.Note})
	}
	objPath := strings.TrimSuffix(path, "/") + "/" + ct.UID + ".vcf"
	obj, err := c.client.PutAddressObject(ctx, objPath, card)
	if err != nil {
		return "", fmt.Errorf("put contact: %w", err)
	}
	if obj != nil {
		return obj.Path, nil
	}
	return objPath, nil
}

// DeleteContact deletes the contact at path.
func (c *CardDAV) DeleteContact(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, resolveURL(c.account.Endpoint, path), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.account.Username, c.account.Password)
	resp, err := c.account.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("DELETE %s: %s", path, resp.Status)
	}
	return nil
}

func (c *CardDAV) resolveBook(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	if len(c.Books) == 0 {
		return "", fmt.Errorf("no address book found")
	}
	return c.Books[0].Path, nil
}

func addressObjectContact(obj carddav.AddressObject) Contact {
	ct := Contact{Path: obj.Path}
	if obj.Card == nil {
		return ct
	}
	if f := obj.Card.Get(vcard.FieldUID); f != nil {
		ct.UID = f.Value
	}
	if f := obj.Card.Get("FN"); f != nil {
		ct.FullName = f.Value
	}
	if f := obj.Card.Get(vcard.FieldEmail); f != nil {
		ct.Email = f.Value
	}
	if f := obj.Card.Get(vcard.FieldTelephone); f != nil {
		ct.Phone = f.Value
	}
	if f := obj.Card.Get(vcard.FieldOrganization); f != nil {
		ct.Org = f.Value
	}
	if f := obj.Card.Get(vcard.FieldNote); f != nil {
		ct.Note = f.Value
	}
	return ct
}
