package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/765362546/novel-reader-go/internal/config"
	"github.com/765362546/novel-reader-go/internal/httpapi"
	"github.com/765362546/novel-reader-go/internal/logging"
)

func main() {
	app := config.Load()
	logging.Setup(app.Debug || app.DebugLog)
	slog.Info("starting novel-reader-go",
		"port", app.Port,
		"storage", app.StorageRoot(),
		"secure", app.Secure,
		"debug", app.Debug,
	)
	srv := httpapi.New(app)

	// Gracefully drain in-flight requests on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		slog.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("graceful shutdown failed", "error", err)
		}
	}()

	if err := srv.Run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}
