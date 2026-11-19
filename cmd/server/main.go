package main

import (
	"log/slog"
	"os"

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
	if err := httpapi.New(app).Run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}
