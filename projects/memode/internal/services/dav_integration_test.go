package services

import (
	"context"
	"testing"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/davtest"
	"codebase.bid/projects/memode/internal/services/calendar"
	"codebase.bid/projects/memode/internal/services/contacts"
)

// TestDAVServiceRoundTrip drives the calendar and contacts services over HTTP
// against live DAV servers, through the same Lua surface memode.write exposes.
func TestDAVServiceRoundTrip(t *testing.T) {
	ctx := context.Background()
	calServer := davtest.CalServer()
	cardServer := davtest.CardServer()
	t.Cleanup(calServer.Close)
	t.Cleanup(cardServer.Close)
	t.Setenv("MEMODE_CALDAV_ENDPOINT", calServer.URL+"/")
	t.Setenv("MEMODE_CARDDAV_ENDPOINT", cardServer.URL+"/")

	cfg := config.Config{}
	cfg.ICloudUser, cfg.ICloudPassword = "owner@example.com", "app-password"

	reg := &codemode.Registry{}
	calSvc := calendar.New(cfg)
	contactsSvc := contacts.New(cfg)
	ready := map[string]bool{}
	calSvc.Setup(ctx, func(ok bool) { ready["calendar"] = ok })
	contactsSvc.Setup(ctx, func(ok bool) { ready["contacts"] = ok })
	if !ready["calendar"] || !ready["contacts"] {
		t.Fatalf("setup readiness: %#v", ready)
	}
	calSvc.Register(reg)
	contactsSvc.Register(reg)

	for _, tc := range []struct{ name, script string }{
		{"calendar", calendarScript},
		{"contacts", contactsScript},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := codemode.Run(ctx, reg, codemode.AllowAll, tc.script, codemode.RunOptions{})
			if err != nil {
				t.Fatalf("script: %v\noutput: %v", err, res.Items)
			}
			if len(res.Items) != 1 {
				t.Fatalf("expected one result item, got %#v", res.Items)
			}
		})
	}
}

// calendarScript exercises create, time-range read, get, update and delete.
const calendarScript = `
local function check(cond, msg) if not cond then error("check failed: " .. msg) end end

local r = calendar.upsert_event({summary = "Kumquat tasting", location = "Orchard",
    start = "2026-11-03T09:00:00Z", end_time = "2026-11-03T11:00:00Z"})
check(r.path:sub(-4) == ".ics", "path should end in .ics, got " .. tostring(r.path))
check(r.uid ~= nil and #r.uid > 0, "uid")

local from, to = "2026-11-03T08:00:00Z", "2026-11-03T10:00:00Z"
local evs = calendar.events({from = from, to = to})
check(#evs == 1, "one event in range, got " .. #evs)
check(evs[1].summary == "Kumquat tasting", "summary")
check(evs[1].location == "Orchard", "location")

local one = calendar.get({path = evs[1].path})
check(one.uid == r.uid, "get uid")
check(one.start ~= nil, "get start")
check(one.end_time ~= nil, "get end_time")

calendar.upsert_event({uid = r.uid, summary = "Kumquat tasting (revised)",
    start = "2026-11-03T09:00:00Z", end_time = "2026-11-03T12:00:00Z"})
evs = calendar.events({from = from, to = to})
check(#evs == 1, "still one event after update")
check(evs[1].summary == "Kumquat tasting (revised)", "updated summary")

check(calendar.delete_event({path = r.path}) == "deleted", "delete result")
evs = calendar.events({from = from, to = to})
check(#evs == 0, "event gone after delete")
return "calendar ok"
`

// contactsScript exercises create, list, search, get, update and delete.
const contactsScript = `
local function check(cond, msg) if not cond then error("check failed: " .. msg) end end

local groups = contacts.groups()
check(#groups == 1, "one address book")

local r = contacts.upsert({full_name = "Bob Banana", email = "bob@example.com",
    phone = "+15550000", org = "Fruit Co"})
check(r.path:sub(-4) == ".vcf", "contact path should end in .vcf, got " .. tostring(r.path))
check(r.uid ~= nil and #r.uid > 0, "contact uid")

local all = contacts.list({})
check(#all == 1, "one contact, got " .. #all)
check(all[1].full_name == "Bob Banana", "full name")
check(all[1].email == "bob@example.com", "email")
check(all[1].phone == "+15550000", "phone")

local found = contacts.search({query = "banana"})
check(#found == 1, "search by name")
found = contacts.search({query = "FRUIT CO"})
check(#found == 1, "search is case-insensitive")
found = contacts.search({query = "nobody"})
check(#found == 0, "no false positive")

local one = contacts.get({path = all[1].path})
check(one.org == "Fruit Co", "get org")

contacts.upsert({uid = r.uid, full_name = "Bob B. Banana", org = "Fruit International"})
all = contacts.list({})
check(#all == 1, "still one contact after update")
check(all[1].full_name == "Bob B. Banana", "updated name")
check(all[1].org == "Fruit International", "updated org")

check(contacts.delete({path = r.path}) == "deleted", "delete result")
all = contacts.list({})
check(#all == 0, "contact gone after delete")
return "contacts ok"
`
