package o11y

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/go-chi/httplog/v3"
	"github.com/go-faster/errors"
	slogjournal "github.com/systemd/slog-journal"
)

// setupLogging builds a slog logger whose handler is selected by LOG_FORMAT
// (text, json, or journal; default text) and tagged with the service name and
// version.
func setupLogging(serviceName, serviceVersion string) (*slog.Logger, error) {
	return newLogger(
		serviceName,
		serviceVersion,
		os.Getenv("LOG_FORMAT"),
		os.Stdout,
		func(opts *slogjournal.Options) (slog.Handler, error) {
			return slogjournal.NewHandler(opts)
		},
	)
}

func newLogger(
	serviceName, serviceVersion, format string,
	output io.Writer,
	newJournalHandler func(*slogjournal.Options) (slog.Handler, error),
) (*slog.Logger, error) {
	logFormat := httplog.SchemaOTEL

	var h slog.Handler
	switch strings.ToLower(format) {
	case "journal":
		var err error
		h, err = newJournalHandler(&slogjournal.Options{
			ReplaceGroup: func(k string) string {
				return strings.ReplaceAll(strings.ToUpper(k), "-", "_")
			},
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				a = logFormat.ReplaceAttr(groups, a)
				a.Key = strings.ReplaceAll(strings.ToUpper(a.Key), "-", "_")
				a.Key = strings.ReplaceAll(a.Key, ".", "_")
				return a
			},
		})
		if err != nil {
			return nil, errors.Wrap(err, "create journal handler")
		}
	case "json":
		h = slog.NewJSONHandler(output, &slog.HandlerOptions{
			ReplaceAttr: logFormat.ReplaceAttr,
		})
	default:
		h = slog.NewTextHandler(output, &slog.HandlerOptions{
			ReplaceAttr: logFormat.ReplaceAttr,
		})
	}

	return slog.New(h).With(
		slog.String("app", serviceName),
		slog.String("version", serviceVersion),
	), nil
}
