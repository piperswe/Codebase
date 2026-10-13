package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/oauthstore"
)

// nsService maps a codemode namespace to the service that governs access to
// it. Bare globals (status, help, print, sleep) need no grant.
var nsService = map[string]string{
	"mail":     "mail",
	"calendar": "calendar",
	"contacts": "contacts",
	"telegram": "telegram",
	"kagi":     "search",
}

// scriptArgs is the input of both code-mode tools.
type scriptArgs struct {
	Script         string `json:"script" jsonschema:"Lua 5.2 source. Call the registered service functions and print() or return results. Call help() once to list available functions with descriptions."`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty" jsonschema:"cap on serialized output, default 1 MiB; exceeding it truncates the result"`
	TimeoutMs      int    `json:"timeout_ms,omitempty" jsonschema:"hard deadline for the whole script, default 30000"`
}

// addTool registers memode.read (write=false) or memode.write (write=true).
func (s *Server) addTool(ms *mcp.Server, write bool) {
	name, desc := "memode.read",
		"Execute a Lua script with READ-ONLY access to the owner's personal services. Only non-mutating functions are available. Output is the script's print() lines and return value."
	if write {
		name, desc = "memode.write",
			"Execute a Lua script with READ and WRITE access to the owner's personal services. Mutating functions (send mail, create events, ...) are available for services the agent was granted write on. Output is the script's print() lines and return value."
	}
	tool := &mcp.Tool{Name: name, Description: desc}
	if !write {
		ro, open := true, true
		tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: ro, OpenWorldHint: &open}
	}
	mcp.AddTool(ms, tool, func(ctx context.Context, req *mcp.CallToolRequest, args scriptArgs) (*mcp.CallToolResult, any, error) {
		return s.runCode(ctx, write, args)
	})
}

// runCode executes one script under the effective grants.
func (s *Server) runCode(ctx context.Context, writeTool bool, args scriptArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Script) == "" {
		return nil, nil, fmt.Errorf("script is required")
	}
	grants := oauthstore.GrantsFromContext(ctx)
	if grants == nil {
		// No OAuth middleware in front (local trusted mode): everything
		// configured is fully available.
		grants = s.cfg.FullGrants()
	}
	allow := func(f codemode.Function) bool {
		if svc, ok := nsService[f.Namespace]; ok {
			level := grants[svc]
			if level == "" {
				return false
			}
			return !f.Write || (writeTool && level == "write")
		}
		return true // bare globals
	}
	opts := codemode.RunOptions{}
	if args.MaxOutputBytes > 0 {
		opts.MaxOutputBytes = args.MaxOutputBytes
	}
	if args.TimeoutMs > 0 {
		opts.Timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	res, err := codemode.Run(ctx, s.reg, allow, args.Script, opts)
	var text string
	if err != nil {
		text = "Script failed: " + err.Error()
		if len(res.Items) > 0 {
			text += "\n\nPartial output:\n" + strings.Join(res.Items, "\n\n")
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: text}},
			StructuredContent: map[string]any{"ok": false, "error": err.Error(), "items": res.Items, "truncated": res.Truncated},
			IsError:           true,
		}, nil, nil
	}
	if len(res.Items) == 0 {
		text = "Script completed with no output."
	} else {
		text = strings.Join(res.Items, "\n\n")
	}
	if res.Truncated {
		text += "\n\n(output truncated; raise max_output_bytes or print less)"
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: map[string]any{"ok": true, "items": res.Items, "truncated": res.Truncated},
	}, nil, nil
}
