// Command dashboard runs the HTTP server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/services/auth"
	"dashboard/internal/services/themes"
	"dashboard/internal/settings"
	"dashboard/internal/web"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg := settings.Load()

	masterKey := cfg.MasterKey
	if masterKey == "" {
		slog.Error("MASTER_KEY is required (see docker-compose.example.yml)")
		os.Exit(1)
	}
	crypto.Init(masterKey)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		slog.Error("create data dir", "err", err)
		os.Exit(1)
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	if _, err := auth.EnsureSetupCode(database); err != nil {
		slog.Error("ensure setup code", "err", err)
		os.Exit(1)
	}
	if _, err := themes.EnsureBuiltin(database); err != nil {
		slog.Error("ensure builtin theme", "err", err)
		os.Exit(1)
	}

	deps := web.Deps{DB: database, Settings: cfg}
	mux := http.NewServeMux()
	deps.RegisterAuthRoutes(mux)
	deps.RegisterBoardRoutes(mux)
	deps.RegisterThemeRoutes(mux)

	server := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		slog.Info("listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = server.Shutdown(ctx)
}
