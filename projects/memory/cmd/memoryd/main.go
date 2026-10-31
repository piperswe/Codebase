// memoryd is the single application image entrypoint. Administration is an
// explicit local command path and is never exposed as a tool or worker callback.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"codebase.bid/lib/go/unixsocket"
	"codebase.bid/projects/memory/internal/auth"
	"codebase.bid/projects/memory/internal/config"
	memorymcp "codebase.bid/projects/memory/internal/mcp"
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	"codebase.bid/projects/memory/internal/version"
	"codebase.bid/projects/memory/internal/worker"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := run(ctx, os.Args[1:]); e != nil {
		slog.Error("memoryd command failed", "error", e)
		os.Exit(1)
	}
}
func printJSON(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: memoryd serve|worker|migrate|healthcheck|admin|version|smoke")
	}
	command := args[0]
	if command == "admin" && len(args) == 5 && args[1] == "auth" && args[2] == "rotate-key" {
		until, e := auth.RotateKey(args[3], args[4])
		if e != nil {
			return e
		}
		return printJSON(map[string]string{"previousKeyUntil": until.Format(time.RFC3339)})
	}
	if command == "version" {
		return printJSON(struct {
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			SDK       string `json:"sdk"`
			RPC       string `json:"rpc"`
			SchemaMin int    `json:"schemaMin"`
			SchemaMax int    `json:"schemaMax"`
			GoVersion string `json:"goVersion"`
		}{version.Version, version.Revision, model.SDKVersion, model.RPCVersion, version.SchemaMin, version.SchemaMax, runtime.Version()})
	}
	if command == "admin" && len(args) > 1 && args[1] == "hash-password" {
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 1025))
		if e != nil || len(b) > 1024 {
			return fmt.Errorf("password exceeds limit")
		}
		b = []byte(strings.TrimSuffix(string(b), "\n"))
		if len(b) < 12 {
			return fmt.Errorf("password must have at least 12 bytes")
		}
		h, e := bcrypt.GenerateFromPassword(b, 12)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(os.Stdout, string(h))
		return e
	}
	c, e := config.Load(command)
	if e != nil {
		return e
	}
	if command == "migrate" {
		if len(args) != 2 {
			return fmt.Errorf("migrate requires roles|status|up")
		}
		v, e := postgres.Migrate(ctx, c.MigratorDSN, args[1])
		if e != nil {
			return e
		}
		return printJSON(map[string]int{"schemaVersion": v})
	}
	if command == "worker" {
		return serveWorker(ctx, c)
	}
	if command == "healthcheck" {
		if len(args) != 2 {
			return fmt.Errorf("healthcheck requires api|worker")
		}
		return healthcheck(ctx, c, args[1])
	}
	store, e := postgres.OpenWithPools(ctx, c.AppDSN, c.ReaderDSN, c.CursorKey, int32(c.AppPoolMax), int32(c.ReaderPoolMax))
	if e != nil {
		return e
	}
	defer store.Close()
	switch command {
	case "serve":
		return serve(ctx, c, store)
	case "smoke":
		r, e := store.Read(ctx, operator("personal"), "personal")
		if e != nil {
			return e
		}
		defer r.Close()
		_, e = r.Schema(model.SchemaQuery{Action: "describe", Entity: "entity/type"})
		if e != nil {
			return e
		}
		return printJSON(map[string]string{"status": "ok", "revision": model.Revision(r.Revision)})
	case "admin":
		return admin(ctx, c, store, args[1:])
	default:
		return fmt.Errorf("unknown command")
	}
}
func operator(space string) model.Principal {
	return model.Principal{Actor: "owner", Client: "local-admin", Scopes: []string{"memory:read", "memory:write", "memory:schema:write", "memory:execute"}, Spaces: []string{space}}
}
func admin(ctx context.Context, c config.Config, store *postgres.Store, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("admin requires bootstrap|schema|auth|export|import|hash-password")
	}
	switch args[0] {
	case "bootstrap":
		space := "personal"
		if len(args) > 1 {
			space = args[1]
		}
		return store.Bootstrap(ctx, space)
	case "schema":
		if len(args) != 3 {
			return fmt.Errorf("admin schema propose|apply SPACE; JSON request on stdin")
		}
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if e != nil {
			return e
		}
		if args[1] == "propose" {
			var q model.Proposal
			if e = model.Decode(b, &q); e != nil {
				return e
			}
			if q.Space != args[2] {
				return model.Invalid("space")
			}
			v, e := store.Propose(ctx, operator(q.Space), q)
			if e != nil {
				return e
			}
			return printJSON(v)
		}
		if args[1] == "apply" {
			var q model.Apply
			if e = model.Decode(b, &q); e != nil {
				return e
			}
			if q.Space != args[2] {
				return model.Invalid("space")
			}
			v, e := store.Apply(ctx, operator(q.Space), q)
			if e != nil {
				return e
			}
			return printJSON(v)
		}
	case "auth":
		if len(args) < 2 {
			return fmt.Errorf("admin auth requires register|revoke|cleanup")
		}
		a := &auth.Server{DB: store.App}
		switch args[1] {
		case "revoke":
			if len(args) != 3 {
				return model.Invalid("grant/client id")
			}
			return a.Revoke(ctx, args[2])
		case "cleanup":
			return a.Cleanup(ctx)
		case "register":
			if len(args) != 5 {
				return fmt.Errorf("admin auth register CLIENT_ID NAME REDIRECT_URI")
			}
			return a.Register(ctx, args[2], args[3], []string{args[4]}, time.Time{})
		}
	case "reindex":
		if len(args) != 2 {
			return model.Invalid("space")
		}
		return store.Reindex(ctx, operator(args[1]), args[1])
	case "export":
		if len(args) != 2 {
			return fmt.Errorf("admin export SPACE")
		}
		return store.Export(ctx, operator(args[1]), args[1], os.Stdout)
	case "import":
		if len(args) != 2 {
			return fmt.Errorf("admin import SPACE; versioned export on stdin, empty target space required")
		}
		return store.Import(ctx, args[1], os.Stdin)
	}
	return fmt.Errorf("unknown admin operation")
}
func serveWorker(ctx context.Context, c config.Config) error {
	l, e := unixsocket.Listen(c.WorkerSocket, c.PeerUID)
	if e != nil {
		return e
	}
	defer l.Close()
	server := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.MaxSendMsgSize(1<<20))
	p.RegisterWorkerServer(server, &worker.Server{Version: version.Version, HardStop: func() { os.Exit(124) }})
	go func() { <-ctx.Done(); server.Stop() }()
	return server.Serve(l)
}
func serve(ctx context.Context, c config.Config, store *postgres.Store) error {
	a, e := auth.New(store.App, auth.Config{Issuer: c.PublicURL, Key: c.Key, PreviousKey: c.PreviousKey, PreviousUntil: c.PreviousUntil, OwnerHash: c.OwnerHash, DynamicRegistration: c.DynamicRegistration})
	if e != nil {
		return e
	}
	w, e := worker.Dial(c.WorkerSocket, c.PeerUID)
	if e != nil {
		return e
	}
	defer w.Close()
	w.TimeoutMS = c.CodeTimeoutMS
	w.GraphOperations = c.GraphOperations
	w.ResultBytes = c.ResultBytes
	w.LogBytes = c.LogBytes
	s, e := memorymcp.New(store, w, a, version.Version)
	if e != nil {
		return e
	}
	public, e := net.Listen("tcp", c.HTTP)
	if e != nil {
		return e
	}
	defer public.Close()
	var private net.Listener
	if strings.HasPrefix(c.Admin, "unix:") {
		private, e = unixsocket.Listen(strings.TrimPrefix(c.Admin, "unix:"), -1)
	} else {
		private, e = net.Listen("tcp", c.Admin)
	}
	if e != nil {
		return e
	}
	defer private.Close()
	servers := []*http.Server{{Handler: s.Handler(), ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}, {Handler: s.AdminHandler(), ReadTimeout: 2 * time.Second, ReadHeaderTimeout: time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}}
	errs := make(chan error, 2)
	go func() { errs <- servers[0].Serve(public) }()
	go func() { errs <- servers[1].Serve(private) }()
	// Cleanup is independent of transport sessions and persists across restarts.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = a.Cleanup(ctx)
			}
		}
	}()
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdown)
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func healthcheck(ctx context.Context, c config.Config, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if target == "worker" {
		w, e := worker.Dial(c.WorkerSocket, c.PeerUID)
		if e != nil {
			return e
		}
		defer w.Close()
		return w.Ready(ctx)
	}
	if target != "api" {
		return fmt.Errorf("unknown probe")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := "http://" + c.Admin + "/readyz"
	if strings.HasPrefix(c.Admin, "unix:") {
		endpoint = "http://admin/readyz"
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return unixsocket.Dial(ctx, strings.TrimPrefix(c.Admin, "unix:"), -1)
		}}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("API unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("API not ready")
	}
	return nil
}
