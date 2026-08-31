package o11y

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	slogjournal "github.com/systemd/slog-journal"
)

func TestNewLoggerTextFormatsAndTagsRecords(t *testing.T) {
	for _, format := range []string{"", "text", "TEXT", "unknown"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := newLogger("catalog", "1.0.0", format, &output, nil)
			if err != nil {
				t.Fatal(err)
			}
			logger.Info("ready", slog.String("request-id", "abc"))
			got := output.String()
			for _, fragment := range []string{
				"severity_text=INFO", "body=ready", "app=catalog", "version=1.0.0", "request-id=abc",
			} {
				if !strings.Contains(got, fragment) {
					t.Errorf("text log %q does not contain %q", got, fragment)
				}
			}
		})
	}
}

func TestNewLoggerJSONFormatsAndTagsRecords(t *testing.T) {
	var output bytes.Buffer
	logger, err := newLogger("catalog", "1.0.0", "JsOn", &output, nil)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("degraded", slog.Int("attempt", 2))

	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON log %q: %v", output.String(), err)
	}
	want := map[string]any{
		"severity_text": "WARN",
		"body":          "degraded",
		"app":           "catalog",
		"version":       "1.0.0",
		"attempt":       float64(2),
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("JSON log %q=%v, want %v", key, got[key], value)
		}
	}
	if _, ok := got["timestamp"]; !ok {
		t.Error("JSON log is missing timestamp")
	}
}

func TestNewLoggerJournalConfiguresFieldNormalization(t *testing.T) {
	var gotOptions *slogjournal.Options
	factory := func(opts *slogjournal.Options) (slog.Handler, error) {
		gotOptions = opts
		return slog.DiscardHandler, nil
	}
	logger, err := newLogger("catalog", "1.0.0", "JOURNAL", nil, factory)
	if err != nil {
		t.Fatal(err)
	}
	if logger == nil || gotOptions == nil {
		t.Fatal("journal handler factory was not used")
	}
	if got := gotOptions.ReplaceGroup("http-request"); got != "HTTP_REQUEST" {
		t.Errorf("normalized group=%q, want HTTP_REQUEST", got)
	}
	tests := []struct {
		groups []string
		attr   slog.Attr
		want   string
	}{
		{attr: slog.String(slog.MessageKey, "ready"), want: "BODY"},
		{attr: slog.String("error.message", "bad"), want: "ERROR_MESSAGE"},
		{groups: []string{"group"}, attr: slog.String("nested-key", "value"), want: "NESTED_KEY"},
	}
	for _, test := range tests {
		if got := gotOptions.ReplaceAttr(test.groups, test.attr).Key; got != test.want {
			t.Errorf("normalized attr %q=%q, want %q", test.attr.Key, got, test.want)
		}
	}
}

func TestNewLoggerWrapsJournalHandlerError(t *testing.T) {
	wantErr := errors.New("journal unavailable")
	_, err := newLogger("catalog", "1.0.0", "journal", nil, func(*slogjournal.Options) (slog.Handler, error) {
		return nil, wantErr
	})
	assertWrappedError(t, err, wantErr, "create journal handler")
}
