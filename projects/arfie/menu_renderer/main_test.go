package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const sampleMenu = `{
  "schema_version":"1", "status":"partial",
  "restaurant":{"name":"Example & Kitchen","city":"Chicago","address":"123 Main St","website":"https://restaurant.example/"},
  "selection_rationale":"Official branch menu", "extracted_at":"2026-09-08T12:00:00Z",
  "menus":[
    {"name":"Dinner","availability":"After 5 pm","notes":["Prices include tax"],"sections":[
      {"name":"Starters","description":"To begin","items":[
        {"name":"Soup","description":"Seasonal vegetables","prices":[
          {"label":"Cup","text":"8.50","amount":"8.50","currency":null},
          {"label":"Bowl","text":"USD 12–14","amount":null,"currency":"USD"}
        ],"option_groups":[
          {"name":"Bread","min_selections":0,"max_selections":1,"options":[
            {"name":"Sourdough","description":"Freshly baked","prices":[{"label":null,"text":"+2","amount":"2","currency":null}]}
          ]}
        ],"dietary_labels":["Vegan"],"allergens":["Wheat"],"notes":["Ask about seasonal changes"],"sources":[{"source_id":"pdf","page":2}]},
        {"name":"Catch of the day","description":null,"prices":[{"label":null,"text":"Market price","amount":null,"currency":null}],"option_groups":[],"dietary_labels":[],"allergens":[],"notes":[],"sources":[{"source_id":"web","page":null}]}
      ]}
    ]},
    {"name":"Lunch","availability":null,"notes":[],"sections":[{"name":"Sandwiches","description":null,"items":[]}]}
  ],
  "sources":[
    {"id":"pdf","url":"https://restaurant.example/menu.pdf","media_type":"application/pdf","retrieved_at":"2026-09-08T11:59:00Z"},
    {"id":"web","url":"https://restaurant.example/menu","media_type":"text/html","retrieved_at":"2026-09-08T11:58:00Z"}
  ],
  "warnings":["Dessert menu unavailable"]
}`

func sampleResult(t *testing.T) Result {
	t.Helper()
	r, err := decodeMenu(strings.NewReader(sampleMenu))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func renderResult(t *testing.T, r Result) (*html.Node, string) {
	t.Helper()
	input, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI(context.Background(), nil, bytes.NewReader(input), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
	doc, err := html.Parse(strings.NewReader(stdout.String()))
	if err != nil {
		t.Fatal(err)
	}
	return doc, stdout.String()
}

func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var text strings.Builder
	walk(n, func(child *html.Node) {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
			text.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(text.String()), " ")
}

func TestMenuContentAndStructure(t *testing.T) {
	doc, output := renderResult(t, sampleResult(t))
	if !strings.HasPrefix(strings.ToLower(output), "<!doctype html>") {
		t.Fatal("missing doctype")
	}
	text := nodeText(doc)
	for _, want := range []string{
		"Example & Kitchen", "Chicago", "123 Main St", "Dinner", "After 5 pm", "Prices include tax", "Starters", "To begin",
		"Soup", "Seasonal vegetables", "Cup: 8.50", "Bowl: USD 12–14", "Bread", "Choose 0–1", "Sourdough", "Freshly baked", "+2",
		"Vegan", "Allergens: Wheat", "Ask about seasonal changes", "Catch of the day", "Market price", "Lunch", "Sandwiches",
		"No items are available in this section.", "Dessert menu unavailable", "This menu extraction is incomplete.",
		"Official branch menu", "2026-09-08T12:00:00Z", "application/pdf", "2026-09-08T11:59:00Z", "(p. 2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	var items, titles []string
	ids := make(map[string]bool)
	var fragments []string
	styleFound, detailsFound, warningOutsideDetails := false, false, false
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "article":
			items = append(items, nodeText(n))
		case "h2":
			titles = append(titles, nodeText(n))
		case "style":
			styleFound = strings.Contains(nodeText(n), "@media print") && strings.Contains(nodeText(n), "@media (max-width: 540px)")
		case "details":
			detailsFound = strings.Contains(nodeText(n), "Official branch menu")
		case "aside":
			warningOutsideDetails = strings.Contains(nodeText(n), "Dessert menu unavailable")
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "details" {
					warningOutsideDetails = false
				}
			}
		case "a":
			if href := attr(n, "href"); strings.HasPrefix(href, "#") {
				fragments = append(fragments, strings.TrimPrefix(href, "#"))
			}
		}
		if id := attr(n, "id"); id != "" {
			if ids[id] {
				t.Errorf("duplicate id %q", id)
			}
			ids[id] = true
		}
	})
	if len(items) != 2 || !strings.HasPrefix(items[0], "Soup") || !strings.HasPrefix(items[1], "Catch of the day") {
		t.Fatalf("items missing or reordered: %v", items)
	}
	if strings.Contains(items[1], "Allergens:") || strings.Contains(items[1], "Vegan") || strings.Contains(items[1], "USD") {
		t.Error("invented dietary or currency information")
	}
	if len(titles) < 2 || titles[0] != "Dinner" || titles[1] != "Lunch" {
		t.Errorf("menus missing or reordered: %v", titles)
	}
	for _, fragment := range fragments {
		if !ids[fragment] {
			t.Errorf("broken internal link #%s", fragment)
		}
	}
	if !styleFound || !detailsFound || !warningOutsideDetails {
		t.Errorf("style=%t details=%t visible warnings=%t", styleFound, detailsFound, warningOutsideDetails)
	}
}

func TestStatusesAndNulls(t *testing.T) {
	for _, status := range []string{"ok", "partial", "not_found", "error"} {
		t.Run(status, func(t *testing.T) {
			r := Result{SchemaVersion: "1", Status: status, Menus: []Menu{}, Sources: []Source{}, Warnings: []string{}}
			doc, output := renderResult(t, r)
			if !strings.Contains(nodeText(doc), "No menu items are available.") || strings.Contains(output, "<nil>") {
				t.Fatal("incorrect empty result")
			}
			if status != "ok" && !strings.Contains(nodeText(doc), statusText(status)) {
				t.Error("missing extraction status explanation")
			}
		})
	}
	r := sampleResult(t)
	r.Menus = []Menu{{Name: "Empty menu"}}
	doc, _ := renderResult(t, r)
	if !strings.Contains(nodeText(doc), "No sections are available for this menu.") {
		t.Error("missing empty menu explanation")
	}
}

func TestEscapingAndLinks(t *testing.T) {
	r := sampleResult(t)
	attack := `<script>alert("x")</script><img src=x onerror=alert(1)>`
	r.Restaurant.Name = attack
	r.Restaurant.Website = new("javascript:alert(1)")
	r.Warnings = []string{attack}
	r.Menus[0].Sections[0].Items[0].Description = &attack
	r.Sources[0].URL = "data:text/html,<script>alert(1)</script>"
	r.Sources[0].ID = attack
	r.Menus[0].Sections[0].Items[0].Sources = []SourceRef{{SourceID: attack}, {SourceID: "missing"}}
	doc, output := renderResult(t, r)
	if !strings.Contains(output, "&lt;script&gt;") || !strings.Contains(nodeText(doc), attack) || !strings.Contains(nodeText(doc), "missing") {
		t.Fatal("untrusted text or unresolved citation not preserved")
	}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "script" || n.Data == "img" || n.Data == "link" {
			t.Errorf("unexpected active/external element %s", n.Data)
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(a.Key, "on") {
				t.Errorf("event handler %s", a.Key)
			}
		}
		if n.Data == "a" {
			href := attr(n, "href")
			if !strings.HasPrefix(href, "#") && !httpURL(href) {
				t.Errorf("unsafe link %q", href)
			}
		}
	})
}

func TestInvalidInput(t *testing.T) {
	for name, input := range map[string]string{
		"empty": "", "malformed": "{", "null": "null", "array": "[]", "missing version": `{"status":"ok"}`,
		"wrong version":   strings.Replace(sampleMenu, `"schema_version":"1"`, `"schema_version":"2"`, 1),
		"unknown status":  strings.Replace(sampleMenu, `"status":"partial"`, `"status":"unknown"`, 1),
		"wrong type":      strings.Replace(sampleMenu, `"min_selections":0`, `"min_selections":"zero"`, 1),
		"second document": sampleMenu + `{}`, "trailing garbage": sampleMenu + "garbage",
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI(context.Background(), nil, strings.NewReader(input), &stdout, &stderr); code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
			}
		})
	}
	if _, err := decodeMenu(strings.NewReader(sampleMenu + "\n \t")); err != nil {
		t.Errorf("trailing whitespace: %v", err)
	}
}

type failedIO struct{}

func (failedIO) Read([]byte) (int, error)  { return 0, errors.New("read failed") }
func (failedIO) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestCLIArgumentsAndIO(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--unknown"}, {"menu.json"}} {
		var stdout, stderr bytes.Buffer
		want := 2
		if args[0] == "--help" {
			want = 0
		}
		if code := runCLI(context.Background(), args, failedIO{}, &stdout, &stderr); code != want || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, &stdout, &stderr)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		input   io.Reader
		output  io.Writer
		message string
	}{
		{"read", context.Background(), failedIO{}, io.Discard, "Reading menu: read failed"},
		{"write", context.Background(), strings.NewReader(sampleMenu), failedIO{}, "Writing HTML: write failed"},
		{"short write", context.Background(), strings.NewReader(sampleMenu), shortWriter{}, "Writing HTML: short write"},
		{"cancelled", ctx, strings.NewReader(sampleMenu), io.Discard, "Rendering menu: context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runCLI(tc.ctx, nil, tc.input, tc.output, &stderr); code != 1 || !strings.Contains(stderr.String(), tc.message) {
				t.Fatalf("code=%d stderr=%s", code, &stderr)
			}
		})
	}
}

func TestSelectionLimits(t *testing.T) {
	for _, tc := range []struct {
		min, max *int
		want     string
	}{
		{nil, nil, ""}, {new(0), new(0), "Choose exactly 0"}, {new(1), new(1), "Choose exactly 1"},
		{new(0), new(2), "Choose 0–2"}, {new(0), nil, "Choose at least 0"}, {nil, new(0), "Choose up to 0"},
	} {
		if got := selectionLimits(OptionGroup{MinSelections: tc.min, MaxSelections: tc.max}); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestHTTPURL(t *testing.T) {
	for _, value := range []string{"https://example.com/menu?x=1&y=2", "http://example.com/menu.pdf#page=2", "HTTPS://example.com/"} {
		if !httpURL(value) {
			t.Errorf("rejected %q", value)
		}
	}
	for _, value := range []string{"", "/menu", "//example.com", "javascript:alert(1)", "data:text/html,hello", "file:///etc/passwd", "https:///menu", "https://example.com/\n"} {
		if httpURL(value) {
			t.Errorf("accepted %q", value)
		}
	}
}
