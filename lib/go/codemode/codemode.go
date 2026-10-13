// Package codemode hosts Lua scripts ("code mode") in a sandboxed go-lua VM.
// A host registers its API as namespaced functions; Run executes one script
// with only the functions an allow-predicate approves, and returns the
// script's output items instead of streaming anything to stdout. Only the
// base, string, table and math Lua libraries are opened: scripts have no
// file, OS, package or network access beyond what the host registers.
//
// The go-lua string library is reduced: string.match, string.gsub and
// string.gmatch are not implemented, and string.find does plain substring
// search only (its "plain" argument is effectively always on, so patterns
// such as "%.ics$" never match). Scripts must use string.sub/string.find
// with literal needles instead of patterns.
package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	lua "github.com/Shopify/go-lua"
)

// Fn is a host function. args is the single Lua table argument (possibly
// empty); the result is pushed back to Lua as-is (nil, bool, number, string,
// []any, map[string]any, or JSON-decodable to those).
type Fn func(ctx context.Context, args map[string]any) (any, error)

// Function is one registered API function, e.g. mail.search.
type Function struct {
	Namespace string // e.g. "mail"; "" for a bare global
	Name      string // e.g. "search"
	Desc      string // shown by help()
	Write     bool   // mutating; hosts may exclude write functions in read-only environments
	Fn        Fn
}

// Registry collects host functions. It is safe to build once at startup and
// read concurrently; Run never mutates it.
type Registry struct {
	functions []Function
}

// Register adds a function. Registering the same namespace/name twice panics:
// a silent override would hide API mistakes.
func (r *Registry) Register(f Function) {
	for _, existing := range r.functions {
		if existing.Namespace == f.Namespace && existing.Name == f.Name {
			panic(fmt.Sprintf("codemode: duplicate function %s.%s", f.Namespace, f.Name))
		}
	}
	r.functions = append(r.functions, f)
}

// Functions returns the registered functions in registration order.
func (r *Registry) Functions() []Function { return r.functions }

// Allow decides which registered functions a given script may call.
type Allow func(f Function) bool

// AllowAll permits every function; useful for tests.
func AllowAll(Function) bool { return true }

// AllowReadOnly permits non-write functions only.
func AllowReadOnly(f Function) bool { return !f.Write }

// RunOptions bounds one script run.
type RunOptions struct {
	MaxOutputBytes int           // default 1 MiB; exceeded output is truncated
	Timeout        time.Duration // default 30 s; the VM aborts at the deadline
}

// Result is what a finished script produced.
type Result struct {
	Items     []string // serialized output items: print() lines and the chunk's return values
	Truncated bool     // output exceeded MaxOutputBytes
}

// errAbort aborts the VM from inside the debug hook.
var errAbort = errors.New("script aborted")

// Run compiles and executes script in a fresh VM whose globals contain the
// allowed functions (as nested tables, e.g. mail.search), an output-collecting
// print, sleep(ms) and help(). Only items and the error message leave the VM.
func Run(ctx context.Context, reg *Registry, allow Allow, script string, opts RunOptions) (Result, error) {
	if allow == nil {
		allow = AllowAll
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = 1 << 20
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	l := lua.NewState()
	lua.Require(l, "_G", lua.BaseOpen, true)
	l.Pop(1)
	lua.Require(l, "string", lua.StringOpen, true)
	l.Pop(1)
	lua.Require(l, "table", lua.TableOpen, true)
	l.Pop(1)
	lua.Require(l, "math", lua.MathOpen, true)
	l.Pop(1)

	h := host{ctx: ctx, opts: opts}
	h.open(reg, allow, l)

	// Abort the VM when the deadline passes: the count hook fires every 1000
	// VM instructions, so a spinning script dies within microseconds of ctx
	// expiring. shortcut: go-lua has no context hook, debug-hook is the way.
	deadline, _ := ctx.Deadline()
	lua.SetDebugHook(l, func(l *lua.State, _ lua.Debug) {
		select {
		case <-ctx.Done():
			l.PushString(ctx.Err().Error())
			l.Error()
		default:
			if time.Now().After(deadline) {
				l.PushString(context.DeadlineExceeded.Error())
				l.Error()
			}
		}
	}, lua.MaskCount, 1000)

	if err := l.Load(strings.NewReader(script), "script", "t"); err != nil {
		return Result{}, err
	}
	if err := l.ProtectedCall(0, lua.MultipleReturns, 0); err != nil {
		if h.truncated {
			return Result{Items: h.items, Truncated: true}, nil
		}
		// Tool calls are real: keep the items printed before the error.
		return Result{Items: h.items, Truncated: h.truncated}, fmt.Errorf("script error: %w", err)
	}

	// The chunk's return values are final output items.
	for i := 1; i <= l.Top(); i++ {
		if err := h.addItem(l, i); err != nil {
			return Result{Items: h.items, Truncated: true}, nil
		}
	}
	return Result{Items: h.items, Truncated: h.truncated}, nil
}

type host struct {
	ctx       context.Context
	opts      RunOptions
	items     []string
	size      int
	truncated bool
}

func (h *host) open(reg *Registry, allow Allow, l *lua.State) {
	// Namespaced functions, e.g. mail.search().
	for _, f := range reg.Functions() {
		if !allow(f) || f.Namespace == "" {
			continue
		}
		l.Global(f.Namespace)
		if l.TypeOf(-1) == lua.TypeNil {
			l.Pop(1)
			l.NewTable()
			l.PushValue(-1)
			l.SetGlobal(f.Namespace)
		}
		fn := f
		l.PushGoFunction(func(l *lua.State) int { return h.call(l, fn) })
		l.SetField(-2, fn.Name)
		l.Pop(1) // the namespace table
	}
	// Bare globals.
	for _, f := range reg.Functions() {
		if !allow(f) || f.Namespace != "" {
			continue
		}
		fn := f
		l.PushGoFunction(func(l *lua.State) int { return h.call(l, fn) })
		l.SetGlobal(fn.Name)
	}

	// print collects items instead of writing stdout.
	l.PushGoFunction(func(l *lua.State) int {
		n := l.Top()
		parts := make([]string, 0, n)
		for i := 1; i <= n; i++ {
			s, _ := l.ToString(i)
			parts = append(parts, s)
		}
		if err := h.addText(strings.Join(parts, "\t")); err != nil {
			l.PushString("output limit exceeded")
			l.Error()
		}
		return 0
	})
	l.SetGlobal("print")

	// help() describes the allowed API.
	l.PushGoFunction(func(l *lua.State) int {
		l.NewTable()
		idx := l.Top()
		for _, f := range reg.Functions() {
			if !allow(f) {
				continue
			}
			if f.Namespace == "" {
				l.PushString(f.Desc)
				l.SetField(idx, f.Name)
				continue
			}
			l.PushString(f.Namespace)
			l.RawGet(idx)
			if l.TypeOf(-1) == lua.TypeNil {
				l.Pop(1)
				l.NewTable()
				l.PushValue(-1)
				l.SetField(idx, f.Namespace)
			}
			t := l.Top()
			l.PushString(f.Desc)
			l.SetField(t, f.Name)
			l.Pop(1)
		}
		return 1
	})
	l.SetGlobal("help")

	// sleep(ms) blocks in Go, bounded and context-aware.
	l.PushGoFunction(func(l *lua.State) int {
		ms := lua.CheckInteger(l, 1)
		if ms > 10_000 {
			ms = 10_000
		}
		if ms < 0 {
			ms = 0
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-h.ctx.Done():
			l.PushString(h.ctx.Err().Error())
			l.Error()
		case <-timer.C:
		}
		return 0
	})
	l.SetGlobal("sleep")
}

// call bridges one registered function: read the argument table, invoke fn,
// push its result.
func (h *host) call(l *lua.State, f Function) int {
	args := map[string]any{}
	if l.Top() >= 1 && l.IsTable(1) {
		l.PushNil()
		for l.Next(1) {
			var key any
			switch l.TypeOf(-2) {
			case lua.TypeString:
				key, _ = l.ToString(-2)
			case lua.TypeNumber:
				n, _ := l.ToNumber(-2)
				key = fmt.Sprint(int64(n))
			default:
				key = fmt.Sprintf("%v", l.TypeOf(-2))
			}
			args[key.(string)] = toGo(l, -1)
			l.Pop(1) // keep key for next
		}
	}
	res, err := f.Fn(h.ctx, args)
	if err != nil {
		l.PushString(err.Error())
		l.Error()
		return 0
	}
	pushGo(l, res)
	return 1
}

// addItem serializes the Lua value at index and appends it as an output item.
func (h *host) addItem(l *lua.State, index int) error {
	return h.addText(serialize(l, index))
}

func (h *host) addText(s string) error {
	h.size += len(s)
	h.items = append(h.items, s)
	if h.size > h.opts.MaxOutputBytes {
		h.truncated = true
		return errAbort
	}
	return nil
}

// serialize renders a Lua value as a JSON-ish string item.
func serialize(l *lua.State, index int) string {
	switch l.TypeOf(index) {
	case lua.TypeString:
		s, _ := l.ToString(index)
		return s
	case lua.TypeNone, lua.TypeNil:
		return "nil"
	default:
		data, err := json.Marshal(toGo(l, index))
		if err != nil {
			return fmt.Sprint(toGo(l, index))
		}
		return string(data)
	}
}
