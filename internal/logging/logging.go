// Package logging configures the process-wide log/slog logger.
package logging

import (
	"context"
	"log/slog"
	"os"
)

// Setup installs a text slog handler writing to stdout. When debug is true the
// level is lowered to Debug so request / rule-engine details are visible.
func Setup(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// Component returns a logger annotated with the given component name.
//
// Package-level loggers are created during init, before Setup runs, so the
// handler is resolved lazily at log time instead of capturing slog.Default().
func Component(name string) *slog.Logger {
	return slog.New(&lazyHandler{}).With("component", name)
}

// lazyHandler forwards every record to the current slog default handler so a
// logger created at init time still honours a later Setup call.
type lazyHandler struct {
	attrs []slog.Attr
}

func (h *lazyHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return slog.Default().Enabled(ctx, level)
}

func (h *lazyHandler) Handle(ctx context.Context, r slog.Record) error {
	rec := r.Clone()
	if len(h.attrs) > 0 {
		rec.AddAttrs(h.attrs...)
	}
	return slog.Default().Handler().Handle(ctx, rec)
}

func (h *lazyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &lazyHandler{attrs: merged}
}

func (h *lazyHandler) WithGroup(name string) slog.Handler {
	return slog.Default().Handler().WithGroup(name)
}
