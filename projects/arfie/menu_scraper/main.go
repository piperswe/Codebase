package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"codebase.bid/lib/go/fetchweb"
	"codebase.bid/lib/go/kagi"
)

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("menu_scraper", flag.ContinueOnError)
	flags.SetOutput(stderr)
	city := flags.String("city", "", "Optional city to narrow down the restaurant")
	providerName := flags.String("provider", "openai", "LLM provider: anthropic, openai, or openrouter")
	model := flags.String("model", "", "Override provider default: Haiku 4.5, GPT 5.6 Luna, or Qwen3.8 27B")
	timeout := flags.Duration("timeout", 5*time.Minute, "Overall deadline, including final reconciliation")
	steps := flags.Int("max-steps", 20, "Maximum agent research steps")
	showSchema := flags.Bool("schema", false, "Print the JSON schema without making API calls")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: menu_scraper [flags] \"Restaurant Name\"\nFlags must precede the restaurant name.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showSchema {
		if _, err := stdout.Write(append(append([]byte{}, menuSchema...), '\n')); err != nil {
			return 1
		}
		return 0
	}
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" || len(flags.Arg(0)) > 500 || len(*city) > 300 || *timeout <= 0 || *steps <= 0 || *steps > 100 || (*model != "" && strings.TrimSpace(*model) == "") {
		flags.Usage()
		return 2
	}
	config, err := resolveProvider(*providerName, strings.TrimSpace(*model))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var result Result
	kagiKey, modelKey := getenv("KAGI_API_KEY"), getenv(config.keyEnv)
	if kagiKey == "" || modelKey == "" {
		result = emptyResult()
		result.Warnings = []string{"KAGI_API_KEY and " + config.keyEnv + " are required"}
	} else {
		// API clients are separate from the uncredentialed website fetcher.
		modelClient := config.newModel(modelKey, http.DefaultClient)
		scraper := &Scraper{model: modelClient, kagi: kagi.New(kagi.Config{APIKey: kagiKey}), fetcher: fetchweb.New(fetchweb.Config{UserAgent: "ArfieMenuScraper/1.0"}), maxSteps: *steps, timeout: *timeout, log: stderr}
		result = scraper.run(ctx, strings.TrimSpace(flags.Arg(0)), strings.TrimSpace(*city))
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "Writing JSON:", err)
		return 1
	}
	if result.Status == "ok" || result.Status == "partial" {
		return 0
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(stderr, warning)
	}
	return 1
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}
