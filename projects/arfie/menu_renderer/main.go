package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("menu_renderer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: menu_renderer < menu.json > menu.html\nRender a version 1 menu JSON document as standalone HTML.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	result, err := decodeMenu(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "Reading menu:", err)
		return 1
	}
	var output bytes.Buffer
	if err := menuPage(result).Render(ctx, &output); err != nil {
		fmt.Fprintln(stderr, "Rendering menu:", err)
		return 1
	}
	if _, err := io.Copy(stdout, &output); err != nil {
		fmt.Fprintln(stderr, "Writing HTML:", err)
		return 1
	}
	return 0
}

func decodeMenu(input io.Reader) (Result, error) {
	var result Result
	decoder := json.NewDecoder(input)
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return result, errors.New("expected exactly one JSON document")
		}
		return result, fmt.Errorf("trailing input: %w", err)
	}
	if result.SchemaVersion != "1" {
		return result, fmt.Errorf("unsupported schema_version %q (expected \"1\")", result.SchemaVersion)
	}
	switch result.Status {
	case "ok", "partial", "not_found", "error":
	default:
		return result, fmt.Errorf("unknown status %q", result.Status)
	}
	return result, nil
}

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
