package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"

	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codebase.bid/lib/go/unixsocket"
	"codebase.bid/projects/memory/internal/auth"
	memorymcp "codebase.bid/projects/memory/internal/mcp"
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/worker"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"codebase.bid/projects/memory/sdk"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}
func readBody(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrf(t *testing.T, body string) string {
	t.Helper()
	m := csrfPattern.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatal("CSRF missing", body)
	}
	return m[1]
}
func TestOAuthMCPWorker(t *testing.T) {
	store, _ := database(t)
	ctx := t.Context()
	typ := definition(t, store, model.Definition{Ident: "type/device", Kind: "type", Description: "Synthetic test device"})
	definition(t, store, model.Definition{Ident: "device/year", Kind: "attribute", Description: "Synthetic model year", ValueType: model.Integer, Cardinality: "one"})
	definition(t, store, model.Definition{Ident: "device/link", Kind: "attribute", Description: "Synthetic graph link", ValueType: model.Ref, Cardinality: "many"})
	dir, e := os.MkdirTemp("/tmp", "memory-ipc-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "worker.sock")
	listener, e := unixsocket.Listen(path, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.MaxSendMsgSize(1<<20))
	p.RegisterWorkerServer(grpcServer, &worker.Server{Version: "test"})
	go grpcServer.Serve(listener)
	t.Cleanup(func() { grpcServer.Stop(); listener.Close() })
	wc, e := worker.Dial(path, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { wc.Close() })
	server := httptest.NewUnstartedServer(nil)
	issuer := "https://" + server.Listener.Addr().String()
	password := "synthetic-owner-password"
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		t.Fatal(e)
	}
	as, e := auth.New(store.App, auth.Config{Issuer: issuer, Key: []byte(strings.Repeat("s", 32)), OwnerHash: string(hash)})
	if e != nil {
		t.Fatal(e)
	}
	if e = as.Register(ctx, "test-client", "Test <script> client", []string{"https://client.example/callback"}, time.Time{}); e != nil {
		t.Fatal(e)
	}
	api, e := memorymcp.New(store, wc, as, "test")
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = api.Handler()
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	params := url.Values{"response_type": {"code"}, "client_id": {"test-client"}, "redirect_uri": {"https://client.example/callback"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {issuer + "/mcp"}, "scope": {strings.Join(auth.Scopes, " ")}, "state": {"test-state"}}
	authorize := issuer + "/authorize?" + params.Encode()
	resp, e := client.Get(authorize)
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode, readBody(t, resp))
	}
	loginURL := issuer + resp.Header.Get("Location")
	resp.Body.Close()
	resp, e = client.Get(loginURL)
	if e != nil {
		t.Fatal(e)
	}
	loginCSRF := csrf(t, readBody(t, resp))
	resp, e = client.PostForm(issuer+"/login", url.Values{"csrf": {loginCSRF}, "password": {password}, "return": {"/authorize?" + params.Encode()}})
	if e != nil || resp.StatusCode != 303 {
		t.Fatal(resp, e)
	}
	resp.Body.Close()
	resp, e = client.Get(authorize)
	if e != nil {
		t.Fatal(e)
	}
	body := readBody(t, resp)
	if strings.Contains(body, "<script>") {
		t.Fatal("client name was not escaped")
	}
	consentCSRF := csrf(t, body)
	consent := url.Values{}
	for k, v := range params {
		consent[k] = v
	}
	consent.Set("csrf", consentCSRF)
	consent.Set("decision", "allow")
	consent["grant_scope"] = auth.Scopes
	consent["grant_space"] = []string{"personal"}
	resp, e = client.PostForm(issuer+"/authorize", consent)
	if e != nil || resp.StatusCode != 303 {
		t.Fatal(resp, e)
	}
	location, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	codeValue := location.Query().Get("code")
	if codeValue == "" || location.Query().Get("state") != "test-state" {
		t.Fatal("authorization failed", location)
	}
	tokenForm := url.Values{"grant_type": {"authorization_code"}, "client_id": {"test-client"}, "code": {codeValue}, "redirect_uri": {"https://client.example/callback"}, "code_verifier": {verifier}, "resource": {issuer + "/mcp"}}
	resp, e = client.PostForm(issuer+"/token", tokenForm)
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 200 {
		t.Fatal(readBody(t, resp))
	}
	var tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if e = json.Unmarshal([]byte(readBody(t, resp)), &tokens); e != nil || tokens.Access == "" {
		t.Fatal(e)
	}
	resp, e = client.PostForm(issuer+"/token", tokenForm)
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("code replay", resp, e)
	}
	resp.Body.Close()
	httpClient := &http.Client{Transport: bearerTransport{base: client.Transport, token: tokens.Access}}
	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "memory-integration", Version: "1"}, nil)
	session, e := sdkClient.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: issuer + "/mcp", HTTPClient: httpClient}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if e != nil || len(tools.Tools) != 10 {
		t.Fatal(tools, e)
	}
	var contracts map[string]struct {
		Output json.RawMessage `json:"output"`
	}
	contractBytes, _ := sdk.Files.ReadFile("contracts.json")
	if e = json.Unmarshal(contractBytes, &contracts); e != nil {
		t.Fatal(e)
	}
	outputSchemas := map[string]*jsonschema.Resolved{}
	for name, c := range contracts {
		var schema jsonschema.Schema
		if e = json.Unmarshal(c.Output, &schema); e != nil {
			t.Fatal(e)
		}
		resolved, e := schema.Resolve(nil)
		if e != nil {
			t.Fatal(e)
		}
		outputSchemas[name] = resolved
	}
	call := func(name string, arg any) model.Envelope {
		t.Helper()
		result, e := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: arg})
		if e != nil {
			t.Fatal(name, e)
		}
		b, _ := json.Marshal(result.StructuredContent)
		var instance any
		if e = json.Unmarshal(b, &instance); e != nil {
			t.Fatal(e)
		}
		if e = outputSchemas[name].Validate(instance); e != nil {
			t.Fatalf("%s output contract: %v", name, e)
		}
		var out model.Envelope
		if e = json.Unmarshal(b, &out); e != nil || result.IsError || !out.OK {
			t.Fatalf("%s: %s (%v)", name, b, e)
		}
		return out
	}
	schemaOut := call("memory_schema", model.SchemaQuery{Space: "personal", Action: "list"})
	schemaRev := schemaOut.Meta.SchemaRevision
	transaction := model.Transaction{Space: "personal", IdempotencyKey: "mcp-create-0001", ExpectedSchemaRevision: schemaRev, Reason: "Synthetic MCP create", Operations: []model.Operation{{Op: "createSource", TempID: "source", Ident: "source/mcp-test", Source: &model.Source{Origin: "user", Description: "Synthetic evidence"}}, {Op: "createEntity", TempID: "entity", Ident: "device/mcp-test", Types: []string{typ}}, {Op: "assert", Entity: model.EntityRef{TempID: "entity"}, Attribute: "device/year", Value: &model.Value{Type: model.Integer, Text: "2024"}, Evidence: []model.Evidence{{Source: "temp:source"}}}}}
	created := call("memory_transact", transaction)
	b, _ := json.Marshal(created.Data)
	var commit model.Commit
	if e = json.Unmarshal(b, &commit); e != nil {
		t.Fatal(e)
	}
	id := commit.TempIDs["entity"]
	call("memory_get", model.Get{Space: "personal", Entities: []string{id, "device/mcp-test"}})
	corrected := transaction
	corrected.IdempotencyKey = "mcp-correct-001"
	corrected.Reason = "Synthetic correction"
	corrected.Operations = []model.Operation{{Op: "replace", Entity: model.EntityRef{ID: id}, ExpectedRevision: commit.Revision, Attribute: "device/year", Value: &model.Value{Type: model.Integer, Text: "2025"}, Evidence: []model.Evidence{{Source: commit.TempIDs["source"]}}}}
	call("memory_transact", corrected)
	for _, filename := range []string{"schema.js", "navigation.js", "batch-pull.js", "incoming.js", "integers.js", "history.js", "pagination.js", "unknown-attribute.js"} {
		t.Run(filename, func(t *testing.T) {
			source, e := sdk.Files.ReadFile("examples/" + filename)
			if e != nil {
				t.Fatal(e)
			}
			input, _ := json.Marshal(map[string]any{"entity": id, "entities": []string{id}, "concept": "year"})
			call("memory_code", model.Code{Space: "personal", Source: string(source), Input: input})
		})
	}
	for _, source := range []string{`function main(){while(true){}}`, `function main(){db.transact({})}`, `function main(){return db.entity("other/missing")}`, `function main(){return undefined}`} {
		result, e := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "memory_code", Arguments: model.Code{Space: "personal", Source: source, Input: json.RawMessage(`null`), TimeoutMS: 50}})
		if e != nil || !result.IsError {
			t.Fatal("script boundary", result, e)
		}
		call("memory_get", model.Get{Space: "personal", Entities: []string{id}})
		call("memory_code", model.Code{Space: "personal", Source: `function main(){return true}`, Input: json.RawMessage(`null`)})
	}
	// Forged protobuf write fields cannot reach a mutation path, even when the
	// authenticated caller has write privileges.
	r, e := store.Read(ctx, owner(), "personal")
	if e != nil {
		t.Fatal(e)
	}
	forged := &p.GraphRequest{CallId: 1}
	forged.ProtoReflect().SetUnknown(protowire.AppendTag(nil, 20, protowire.BytesType))
	res := worker.Dispatch(r, forged)
	r.Close()
	if res.GetError() == nil || res.GetError().Code != "INCOMPATIBLE_PROTOCOL" {
		t.Fatal("forged operation accepted", res)
	}
	grpcServer.Stop()
	listener.Close()
	call("memory_get", model.Get{Space: "personal", Entities: []string{id}})
	failed, e := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "memory_code", Arguments: model.Code{Space: "personal", Source: `function main(){return true}`, Input: json.RawMessage(`null`)}})
	if e != nil || !failed.IsError {
		t.Fatal("worker loss was not bounded", failed, e)
	}
	listener, e = unixsocket.Listen(path, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	grpcServer = grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.MaxSendMsgSize(1<<20))
	p.RegisterWorkerServer(grpcServer, &worker.Server{Version: "restarted"})
	go grpcServer.Serve(listener)
	deadline := time.Now().Add(5 * time.Second)
	for wc.Ready(ctx) != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	call("memory_code", model.Code{Space: "personal", Source: `function main(){return db.entity("device/mcp-test").id}`, Input: json.RawMessage(`null`)})
	// Read-only scope and other-space denials are independent of transport/session IDs.
	readonly := owner()
	readonly.Scopes = []string{"memory:read"}
	readonly.Spaces = []string{"personal"}
	_, e = api.Call(model.WithPrincipal(ctx, readonly), "memory_transact", mustJSON(t, transaction))
	code(t, e, "PERMISSION_DENIED")
	_, e = api.Call(model.WithPrincipal(ctx, readonly), "memory_get", mustJSON(t, model.Get{Space: "other", Entities: []string{id}}))
	code(t, e, "PERMISSION_DENIED")
	if _, e = as.Validate(ctx, tokens.Access); e != nil {
		t.Fatal("positive token before negative tests", e)
	}
	parts := strings.Split(tokens.Access, ".")
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var original map[string]any
	_ = json.Unmarshal(payloadBytes, &original)
	headerBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var header map[string]any
	_ = json.Unmarshal(headerBytes, &header)
	for _, test := range []struct {
		issuer, audience string
		expiry           time.Time
		method           jwt.SigningMethod
	}{{"https://wrong.example", issuer + "/mcp", time.Now().Add(time.Minute), jwt.SigningMethodHS256}, {issuer, "https://wrong.example/mcp", time.Now().Add(time.Minute), jwt.SigningMethodHS256}, {issuer, issuer + "/mcp", time.Now().Add(-time.Minute), jwt.SigningMethodHS256}, {issuer, issuer + "/mcp", time.Now().Add(time.Minute), jwt.SigningMethodHS384}} {
		forged := jwt.NewWithClaims(test.method, jwt.MapClaims{"iss": test.issuer, "aud": test.audience, "exp": test.expiry.Unix(), "sub": "test-client", "grant": original["grant"]})
		forged.Header["kid"] = header["kid"]
		raw, _ := forged.SignedString(as.Config.Key)
		if _, e = as.Validate(ctx, raw); e == nil {
			t.Fatal("invalid JWT accepted")
		}
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {"test-client"}, "refresh_token": {tokens.Refresh}, "resource": {issuer + "/mcp"}}
	resp, e = client.PostForm(issuer+"/token", refreshForm)
	if e != nil || resp.StatusCode != 200 {
		t.Fatal("refresh", resp, e)
	}
	var rotated struct {
		Access string `json:"access_token"`
	}
	json.Unmarshal([]byte(readBody(t, resp)), &rotated)
	resp, e = client.PostForm(issuer+"/token", refreshForm)
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("refresh replay", resp, e)
	}
	resp.Body.Close()
	if _, e = as.Validate(ctx, rotated.Access); e == nil {
		t.Fatal("refresh replay failed to revoke family")
	}
	invalidConsent := url.Values{}
	for k, v := range consent {
		invalidConsent[k] = v
	}
	invalidConsent.Set("csrf", "wrong")
	resp, e = client.PostForm(issuer+"/authorize", invalidConsent)
	if e != nil || resp.StatusCode != 403 {
		t.Fatal("consent CSRF", resp, e)
	}
	resp.Body.Close()
	invalidConsent.Set("csrf", consentCSRF)
	invalidConsent.Set("scope", "memory:read")
	invalidConsent["grant_scope"] = []string{"memory:write"}
	resp, e = client.PostForm(issuer+"/authorize", invalidConsent)
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("scope escalation", resp, e)
	}
	resp.Body.Close()
	invalidRedirect := url.Values{}
	for k, v := range params {
		invalidRedirect[k] = v
	}
	invalidRedirect.Set("redirect_uri", "https://evil.example/callback")
	resp, e = client.Get(issuer + "/authorize?" + invalidRedirect.Encode())
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("redirect mismatch", resp, e)
	}
	resp.Body.Close()
	resp, e = client.PostForm(issuer+"/authorize", consent)
	if e != nil || resp.StatusCode != 303 {
		t.Fatal(resp, e)
	}
	badCodeURL, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	badForm := url.Values{}
	for k, v := range tokenForm {
		badForm[k] = v
	}
	badForm.Set("code", badCodeURL.Query().Get("code"))
	badForm.Set("code_verifier", strings.Repeat("x", 43))
	resp, e = client.PostForm(issuer+"/token", badForm)
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("PKCE mismatch", resp, e)
	}
	resp.Body.Close()
	badForm.Set("code_verifier", verifier)
	resp, e = client.PostForm(issuer+"/token", badForm)
	if e != nil || resp.StatusCode != 400 {
		t.Fatal("failed code reused", resp, e)
	}
	resp.Body.Close()
	for _, path := range []string{"/metrics", "/readyz", "/register"} {
		resp, e = client.Get(issuer + path)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode == 200 {
			t.Fatal("private/disabled endpoint exposed", path)
		}
		resp.Body.Close()
	}
}
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
