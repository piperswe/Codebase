// Package mcp exposes the shared graph services through the official MCP SDK.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"codebase.bid/projects/memory/internal/auth"
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	"codebase.bid/projects/memory/internal/worker"
	"codebase.bid/projects/memory/sdk"
	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	Store              *postgres.Store
	Worker             *worker.Client
	Auth               *auth.Server
	MCP                *mcpsdk.Server
	requests, failures atomic.Uint64
	schemas            map[string]*jsonschema.Resolved
}
type contract struct {
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
	Description string          `json:"description"`
	Scope       string          `json:"scope"`
}

func New(store *postgres.Store, w *worker.Client, a *auth.Server, version string) (*Server, error) {
	s := &Server{Store: store, Worker: w, Auth: a, schemas: map[string]*jsonschema.Resolved{}, MCP: mcpsdk.NewServer(&mcpsdk.Implementation{Name: "memory", Version: version}, &mcpsdk.ServerOptions{SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"}})}
	b, e := sdk.Files.ReadFile("contracts.json")
	if e != nil {
		return nil, e
	}
	var contracts map[string]contract
	if e = json.Unmarshal(b, &contracts); e != nil {
		return nil, e
	}
	for name, c := range contracts {
		name, c := name, c
		var schema jsonschema.Schema
		if e = json.Unmarshal(c.Input, &schema); e != nil {
			return nil, e
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return nil, err
		}
		s.schemas[name] = resolved
		read := c.Scope == "memory:read" || name == "memory_code"
		open := false
		destructive := !read
		idempotent := read || name == "memory_transact"
		s.MCP.AddTool(&mcpsdk.Tool{Name: name, Description: c.Description, InputSchema: c.Input, OutputSchema: c.Output, Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: read, DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &open}}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			s.requests.Add(1)
			ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
			defer cancel()
			out, e := s.Call(ctx, name, req.Params.Arguments)
			if e != nil {
				s.failures.Add(1)
				out.OK = false
				out.Data = nil
				var m *model.Error
				if !errors.As(e, &m) {
					m = model.E("INTERNAL_ERROR", "Operation failed")
				}
				out.Error = m
				out.Meta.Complete = false
			}
			if out.Meta.RequestID == "" {
				out.Meta.RequestID = model.NewID()
			}
			if out.Meta.SpaceRevision == "" {
				out.Meta.SpaceRevision = "0"
			}
			if out.Meta.SchemaRevision == "" {
				out.Meta.SchemaRevision = "0"
			}
			b, _ := json.Marshal(out)
			return &mcpsdk.CallToolResult{IsError: !out.OK, StructuredContent: out, Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}}}, nil
		})
	}
	s.MCP.AddResource(&mcpsdk.Resource{URI: "memory://sdk/code-mode", Name: "Memory Code Mode SDK", MIMEType: "text/plain"}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		p := model.FromContext(ctx)
		if len(p.Spaces) == 0 || !has(p.Scopes, "memory:read") {
			return nil, model.E("PERMISSION_DENIED", "Read scope required")
		}
		b, _ := sdk.Files.ReadFile("code-mode.d.ts")
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{URI: "memory://sdk/code-mode", MIMEType: "text/plain", Text: string(b)}}}, nil
	})
	return s, nil
}
func has(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
func (s *Server) Call(ctx context.Context, name string, raw json.RawMessage) (out model.Envelope, err error) {
	ctx, request := model.EnsureRequest(ctx)
	defer func() { out.Meta.RequestID = request.ID }()
	out.OK = true
	if schema := s.schemas[name]; schema != nil {
		var data any
		if len(raw) > 1<<20 || json.Unmarshal(raw, &data) != nil || schema.Validate(data) != nil {
			return out, model.Invalid("tool input schema")
		}
	}
	p := model.FromContext(ctx)
	switch name {
	case "memory_transact":
		var q model.Transaction
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		v, e := s.Store.Transact(ctx, p, q)
		out.Data = v
		out.Meta = model.Meta{SpaceRevision: v.Revision, SchemaRevision: v.SchemaRevision, Complete: e == nil}
		return out, e
	case "memory_schema_propose":
		var q model.Proposal
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		v, e := s.Store.Propose(ctx, p, q)
		out.Data = v
		out.Meta = model.Meta{SpaceRevision: v.Revision, SchemaRevision: v.SchemaRevision, Complete: e == nil}
		return out, e
	case "memory_schema_apply":
		var q model.Apply
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		v, e := s.Store.Apply(ctx, p, q)
		out.Data = v
		out.Meta = model.Meta{SpaceRevision: v.Revision, SchemaRevision: v.SchemaRevision, Complete: e == nil}
		return out, e
	case "memory_code":
		var q model.Code
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		if s.Worker == nil {
			return out, &model.Error{Code: "WORKER_UNAVAILABLE", Message: "Code Mode worker is unavailable", Retryable: true}
		}
		v, meta, e := s.Worker.Execute(ctx, s.Store, p, q)
		out.Data = v
		out.Meta = meta
		return out, e
	}
	// Decode the complete strict request before opening a scarce reader session.
	var space string
	var run func(*postgres.Session) (any, error)
	switch name {
	case "memory_get":
		var q model.Get
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) { return r.Get(q.Entities, q.Attributes) }
	case "memory_search":
		var q model.Search
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) { return r.Search(q) }
	case "memory_find":
		var q model.Find
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) { return r.Find(q) }
	case "memory_pull":
		var q model.Pull
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) { return r.Pull(q.Entity, q.Pattern) }
	case "memory_history":
		var q model.History
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) { return r.History(q) }
	case "memory_schema":
		var q model.SchemaQuery
		if e := model.Decode(raw, &q); e != nil {
			return out, e
		}
		space = q.Space
		run = func(r *postgres.Session) (any, error) {
			if q.Action == "help" {
				docs, _ := sdk.Files.ReadFile("code-mode.d.ts")
				example, _ := sdk.Files.ReadFile("examples/navigation.js")
				return struct {
					SDKVersion   string `json:"sdkVersion"`
					Declarations string `json:"declarations"`
					Example      string `json:"example"`
				}{model.SDKVersion, string(docs), string(example)}, nil
			}
			return r.Schema(q)
		}
	default:
		return out, model.Invalid("tool name")
	}
	r, e := s.Store.Read(ctx, p, space)
	if e != nil {
		return out, e
	}
	defer r.Close()
	out.Meta = model.Meta{RequestID: model.NewID(), SpaceRevision: model.Revision(r.Revision), SchemaRevision: model.Revision(r.SchemaRevision), Complete: true}
	out.Data, err = run(r)
	if err == nil {
		err = r.CheckBudget()
	}
	switch v := out.Data.(type) {
	case model.Page[model.Hit]:
		out.Meta.Complete = v.Complete
		out.Meta.NextCursor = v.NextCursor
	case model.Page[model.Entity]:
		out.Meta.Complete = v.Complete
		out.Meta.NextCursor = v.NextCursor
	case model.Page[model.Event]:
		out.Meta.Complete = v.Complete
		out.Meta.NextCursor = v.NextCursor
	case model.Page[model.Definition]:
		out.Meta.Complete = v.Complete
		out.Meta.NextCursor = v.NextCursor
	}
	return out, err
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.Auth.Routes(mux)
	transport := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return s.MCP }, &mcpsdk.StreamableHTTPOptions{Stateless: true})
	mux.Handle("/mcp", s.Auth.Middleware(transport))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok\n")) })
	return s.Auth.Guard(mux)
}
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if s.Store.Check(ctx) != nil {
			http.Error(w, "not ready", 503)
			return
		}
		state := struct {
			Graph bool `json:"graph"`
			Code  bool `json:"code"`
		}{true, s.Worker != nil && s.Worker.Ready(ctx) == nil}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE memory_requests_total counter\nmemory_requests_total %d\n# TYPE memory_failures_total counter\nmemory_failures_total %d\n", s.requests.Load(), s.failures.Load())
	})
	return mux
}
