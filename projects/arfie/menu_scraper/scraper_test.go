package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"codebase.bid/lib/go/fetchweb"
	"codebase.bid/lib/go/kagi"
	"github.com/grafana/ai-sdk/providers/openai"
	"github.com/openai/openai-go/v3/option"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(r *http.Request, status int, media string, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func sampleExtraction(id string, page *int) Extraction {
	return Extraction{Warnings: []string{}, Menus: []Menu{{Name: "Dinner", Notes: []string{"Prices include tax"}, Sections: []Section{{Name: "Starters", Items: []Item{{Name: "Soup", Description: new("Seasonal vegetables"), Prices: []Price{{Text: "USD 8.50", Amount: new("8.50"), Currency: new("USD")}}, OptionGroups: []OptionGroup{{Name: "Bread", MinSelections: new(0), MaxSelections: new(1), Options: []MenuOption{{Name: "Sourdough", Prices: []Price{{Text: "+USD 2", Amount: new("2"), Currency: new("USD")}}}}}}, DietaryLabels: []string{"Vegan"}, Allergens: []string{}, Notes: []string{}, Sources: []SourceRef{{SourceID: id, Page: page}}}}}}}}}
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Replay the Responses wire protocol through the real Grafana provider and
// OpenAI Go client, including streaming tool calls and structured output.
func sse(t *testing.T, r *http.Request, tool string, args any, text string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	emit := func(event map[string]any) { fmt.Fprintf(&body, "data: %s\n\n", marshal(t, event)) }
	var item map[string]any
	if tool != "" {
		item = map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": tool, "arguments": ""}
		emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		encoded := string(marshal(t, args))
		emit(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_1", "delta": encoded})
		item["arguments"] = encoded
		item["status"] = "completed"
	} else {
		item = map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}
		emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		emit(map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_1", "delta": text, "logprobs": []any{}})
		item["status"] = "completed"
		item["content"] = []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}
	}
	emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "object": "response", "created_at": 1, "model": "test-model", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	return response(r, 200, "text/event-stream", body.Bytes())
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.White)
		}
	}
	img.Set(8, 8, color.Black)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pdfFixture(scanned bool) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"}
	if scanned {
		objects = append(objects, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>")
		content := "q 100 0 0 100 10 10 cm /Im0 Do Q"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content), "<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Length 3 >>\nstream\n\x00\x00\x00\nendstream")
	} else {
		objects = append(objects, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>")
		content := "BT /F1 12 Tf 10 100 Td (Soup USD 8.50) Tj ET"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content), "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(offsets), xref)
	return b.Bytes()
}

func TestScraperThroughGrafanaAdapter(t *testing.T) {
	for _, kind := range []string{"html", "image", "pdf", "scanned_pdf", "partial", "bad_merge", "timeout", "malformed", "not_found", "step_limit"} {
		t.Run(kind, func(t *testing.T) {
			media, asset := "text/html", []byte(`<h1>Dinner</h1><h2>Starters</h2><table><tr><td>Soup</td><td>USD 8.50</td></tr></table>`)
			var page *int
			switch kind {
			case "image":
				media, asset = "image/png", pngFixture(t)
			case "pdf", "scanned_pdf":
				media, asset = "application/pdf", pdfFixture(kind == "scanned_pdf")
				page = new(1)
			}
			fragment := sampleExtraction("s2", page)
			var researchCalls, extractionCalls, fetchCalls, kagiCalls atomic.Int32
			webClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				fetchCalls.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("credentials leaked to website")
				}
				if r.URL.Path == "/" {
					return response(r, 200, "text/html", []byte(`<h1>Example Restaurant Chicago</h1><a href="/menu">Menu</a><img data-src="/menu" alt="Menu"><iframe src="/menu"></iframe>`)), nil
				}
				return response(r, 200, media, asset), nil
			})}
			kagiClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				kagiCalls.Add(1)
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer kagi-test" {
					t.Error("incorrect Kagi request")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["query"] != "Example Restaurant Chicago restaurant menu" || body["workflow"] != "search" {
					t.Errorf("Kagi body: %v", body)
				}
				return response(r, 200, "application/json", []byte(`{"data":{"search":[{"url":"https://restaurant.example/","title":"Example Restaurant"}]}}`)), nil
			})}
			modelClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer openai-test" {
					t.Errorf("model request: %s", r.URL)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					return nil, err
				}
				if payload["stream"] != true || payload["store"] != false {
					t.Errorf("expected stateless streaming: %s", body)
				}
				tools, _ := payload["tools"].([]any)
				if len(tools) > 0 {
					n := researchCalls.Add(1)
					if n > 1 && !bytes.Contains(body, []byte("function_call_output")) {
						t.Error("tool result missing from continuation")
					}
					switch n {
					case 1:
						return sse(t, r, "inspect_url", urlInput{"https://restaurant.example/"}, ""), nil
					case 2:
						return sse(t, r, "select_restaurant", selectionInput{"s1", "Example Restaurant", "Chicago", "123 Main St", "https://restaurant.example/", "Best name and city match, verified on official website"}, ""), nil
					case 3:
						return sse(t, r, "inspect_url", urlInput{"https://restaurant.example/menu"}, ""), nil
					case 4:
						return sse(t, r, "extract_menu", sourceInput{"s2"}, ""), nil
					case 5:
						if kind == "malformed" {
							return sse(t, r, "extract_menu", sourceInput{"s2"}, ""), nil
						}
						if kind == "partial" {
							return sse(t, r, "report_gap", gapInput{"Drinks PDF is inaccessible"}, ""), nil
						}
						if kind == "timeout" {
							<-r.Context().Done()
							return nil, r.Context().Err()
						}
					}
					return sse(t, r, "", nil, "Finished researching menus"), nil
				}
				n := extractionCalls.Add(1)
				if !bytes.Contains(body, []byte(`"json_schema"`)) || !bytes.Contains(body, []byte(`"strict":true`)) {
					t.Error("structured output missing")
				}
				if n == 1 {
					if media == "application/pdf" {
						if !bytes.Contains(body, []byte("input_file")) || !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(asset))) {
							t.Error("PDF bytes missing from native file input")
						}
					} else if media == "image/png" {
						if !bytes.Contains(body, []byte("input_image")) || !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(asset))) {
							t.Error("image bytes missing from native image input")
						}
					} else if !bytes.Contains(body, []byte("Soup")) {
						t.Error("HTML text missing")
					}
				}
				if n == 2 && kind == "bad_merge" {
					return sse(t, r, "", nil, `{"menus":[],"warnings":[]}`), nil
				}
				if kind == "malformed" {
					return sse(t, r, "", nil, `{"menus":"invalid","warnings":[]}`), nil
				}
				if kind == "not_found" {
					return sse(t, r, "", nil, `{"menus":[],"warnings":[]}`), nil
				}
				return sse(t, r, "", nil, string(marshal(t, fragment))), nil
			})}
			model := openai.NewResponses("openai-test", "test-model", openai.WithRequestOptions(option.WithBaseURL("https://model.example/"), option.WithHTTPClient(modelClient), option.WithMaxRetries(0)))
			timeout := 10 * time.Second
			if kind == "timeout" {
				timeout = 500 * time.Millisecond
			}
			s := &Scraper{model: model, kagi: kagi.New(kagi.Config{HTTPClient: kagiClient, Endpoint: "https://kagi.example/search", APIKey: "kagi-test"}), fetcher: fetchweb.New(fetchweb.Config{HTTPClient: webClient}), maxSteps: 20, timeout: timeout, log: io.Discard}
			if kind == "step_limit" {
				s.maxSteps = 4
			}
			got := s.run(context.Background(), "Example Restaurant", "Chicago")
			wantStatus := "ok"
			if kind == "partial" || kind == "bad_merge" || kind == "timeout" || kind == "step_limit" {
				wantStatus = "partial"
			}
			wantItems, wantExtractions := 1, int32(2)
			if kind == "malformed" {
				wantStatus = "error"
				wantItems = 0
				wantExtractions = 1
			}
			if kind == "not_found" {
				wantStatus = "not_found"
				wantItems = 0
				wantExtractions = 1
			}
			if got.Status != wantStatus {
				t.Fatalf("status %s, warnings %v", got.Status, got.Warnings)
			}
			if itemCount(got.Menus) != wantItems || got.Restaurant == nil || got.Restaurant.City == nil || *got.Restaurant.City != "Chicago" {
				t.Fatalf("unexpected result: %+v", got)
			}
			if err := validateResult(got); err != nil {
				t.Fatal(err)
			}
			if extractionCalls.Load() != wantExtractions || fetchCalls.Load() != 2 || kagiCalls.Load() != 1 {
				t.Errorf("calls: extraction=%d fetch=%d kagi=%d", extractionCalls.Load(), fetchCalls.Load(), kagiCalls.Load())
			}
		})
	}
}

func TestSchemaAndMerge(t *testing.T) {
	r := emptyResult()
	r.Menus = sampleExtraction("s1", nil).Menus
	r.Sources = []fetchweb.Source{{ID: "s1", URL: "https://restaurant.example/menu", MediaType: "text/html", RetrievedAt: r.ExtractedAt}}
	if err := validateResult(r); err != nil {
		t.Fatal(err)
	}
	merged := sampleExtraction("s1", nil)
	merged.Menus[0].Sections[0].Items[0].Prices[0].Amount = new("9000")
	if err := validateMerge(r, merged); err == nil {
		t.Fatal("invented price accepted")
	}
	merged = sampleExtraction("missing", nil)
	if err := validateMerge(r, merged); err == nil {
		t.Fatal("invented citation accepted")
	}
	merged = sampleExtraction("s1", new(1))
	check := r
	check.Menus = merged.Menus
	if err := validateResult(check); err == nil {
		t.Fatal("HTML page number accepted")
	}
	merged = sampleExtraction("s1", nil)
	merged.Menus[0].Sections[0].Items[0].Prices[0].Amount = new("NaN")
	check.Menus = merged.Menus
	if err := validateResult(check); err == nil {
		t.Fatal("invalid decimal accepted")
	}
	duplicate := r
	r.Menus = append(r.Menus, duplicate.Menus...)
	if err := validateMerge(r, sampleExtraction("s1", nil)); err != nil {
		t.Fatalf("valid dedup rejected: %v", err)
	}
}

func TestCLI(t *testing.T) {
	for _, tc := range []struct {
		args []string
		exit int
		json bool
	}{{[]string{"--help"}, 0, false}, {[]string{"--schema"}, 0, true}, {nil, 2, false}, {[]string{"--max-steps", "0", "Cafe"}, 2, false}, {[]string{"--timeout", "0s", "Cafe"}, 2, false}, {[]string{"Cafe"}, 1, true}} {
		var stdout, stderr bytes.Buffer
		code := runCLI(context.Background(), tc.args, &stdout, &stderr, func(string) string { return "" })
		if code != tc.exit {
			t.Errorf("args %v: exit %d; %s", tc.args, code, stderr.String())
		}
		if tc.json && !json.Valid(stdout.Bytes()) {
			t.Errorf("invalid stdout JSON: %s", stdout.String())
		}
		if !tc.json && stdout.Len() != 0 {
			t.Errorf("unexpected stdout: %s", stdout.String())
		}
	}
}
