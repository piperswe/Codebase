package codemode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testRegistry() *Registry {
	reg := &Registry{}
	reg.Register(Function{Namespace: "fake", Name: "echo", Desc: "echo args", Fn: func(_ context.Context, args map[string]any) (any, error) {
		return args, nil
	}})
	reg.Register(Function{Namespace: "fake", Name: "get", Desc: "return a struct", Fn: func(context.Context, map[string]any) (any, error) {
		return map[string]any{"list": []any{1, "two", true}, "nested": map[string]any{"k": 2.5}}, nil
	}})
	reg.Register(Function{Namespace: "fake", Name: "boom", Desc: "always fails", Fn: func(context.Context, map[string]any) (any, error) {
		return nil, errors.New("upstream exploded")
	}})
	reg.Register(Function{Namespace: "fake", Name: "empty", Desc: "nil slice and map", Fn: func(context.Context, map[string]any) (any, error) {
		var list []string
		var m map[string]any
		return map[string]any{"list": list, "m": m}, nil
	}})
	reg.Register(Function{Namespace: "fake", Name: "destroy", Desc: "a write op", Write: true, Fn: func(context.Context, map[string]any) (any, error) {
		return "destroyed", nil
	}})
	reg.Register(Function{Name: "ping", Desc: "bare global", Fn: func(context.Context, map[string]any) (any, error) {
		return "pong", nil
	}})
	return reg
}

func TestRunBasic(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), nil, `
		local r = fake.get()
		return r.list[2]
	`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0] != "two" {
		t.Fatalf("unexpected items: %#v", res.Items)
	}
}

func TestRunPrintAndReturn(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), nil, `
		print("a", 42)
		return fake.echo({x = 1})
	`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Items) != 2 || res.Items[0] != "a\t42" || !strings.Contains(res.Items[1], `"x":1`) {
		t.Fatalf("unexpected items: %#v", res.Items)
	}
}

func TestReadOnlyEnvExcludesWrite(t *testing.T) {
	_, err := Run(context.Background(), testRegistry(), AllowReadOnly, `return fake.destroy()`, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "script error") {
		t.Fatalf("expected error calling write fn in read env, got %v", err)
	}
	if _, err := Run(context.Background(), testRegistry(), AllowReadOnly, `return fake.get()`, RunOptions{}); err != nil {
		t.Fatalf("read fn should work: %v", err)
	}
	res, err := Run(context.Background(), testRegistry(), AllowReadOnly, `return fake.destroy ~= nil`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items[0] != "false" {
		t.Fatalf("write fn should not exist in read-only env, got %s", res.Items[0])
	}
}

func TestTimeoutAbortsInfiniteLoop(t *testing.T) {
	start := time.Now()
	_, err := Run(context.Background(), testRegistry(), nil, `while true do end`, RunOptions{Timeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout took too long: %v", time.Since(start))
	}
}

func TestOutputCap(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), nil, `
		for i = 1, 100 do print(string.rep("x", 1000)) end
	`, RunOptions{MaxOutputBytes: 5000})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected truncation")
	}
	total := 0
	for _, it := range res.Items {
		total += len(it)
	}
	if total > 5000+2000 { // one item may overshoot the cap before it is noticed
		t.Fatalf("output far exceeds cap: %d", total)
	}
}

func TestFnError(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), nil, `
		print("before")
		fake.boom()
	`, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("expected fn error, got %v", err)
	}
	if len(res.Items) != 1 || res.Items[0] != "before" {
		t.Fatalf("items before error should be kept: %#v", res.Items)
	}
}

func TestEmptyCollectionsAreTables(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), nil, `
		local r = fake.empty()
		return #r.list .. ":" .. tostring(next(r.m) == nil)
	`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items[0] != "0:true" {
		t.Fatalf("empty collections should be empty tables, got %q", res.Items[0])
	}
}

func TestHelp(t *testing.T) {
	res, err := Run(context.Background(), testRegistry(), AllowReadOnly, `return help()`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(res.Items, "\n")
	for _, want := range []string{"echo", "get", "boom", "ping"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("help missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "destroy") {
		t.Fatalf("help must not list write fns in read-only env")
	}
}

func TestNoStdout(t *testing.T) {
	// print must collect, not write stdout; the base library os/io are absent.
	res, err := Run(context.Background(), testRegistry(), nil, `return os ~= nil or io ~= nil`, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items[0] != "false" {
		t.Fatalf("os/io should not be available, got %s", res.Items[0])
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	reg := &Registry{}
	reg.Register(Function{Name: "x", Fn: func(context.Context, map[string]any) (any, error) { return nil, nil }})
	reg.Register(Function{Name: "x", Fn: func(context.Context, map[string]any) (any, error) { return nil, nil }})
}

func TestSleep(t *testing.T) {
	start := time.Now()
	if _, err := Run(context.Background(), testRegistry(), nil, `sleep(50)`, RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatalf("sleep did not sleep: %v", time.Since(start))
	}
	// Capped sleep: asking for 60s must not blow the script deadline.
	_, err := Run(context.Background(), testRegistry(), nil, `sleep(60000)`, RunOptions{Timeout: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected deadline error, got %v", err)
	}
}
