// spark-server starts the local Spark/Particle-compatible cloud service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sparkserver/internal/app"
	"sparkserver/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load("settings.json")
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Error("load config", "error", err)
			os.Exit(1)
		}

		cfg = config.Default()
		logger.Info("settings.json not found, using defaults")
	}

	server := app.New(cfg, logger)
	// Start wires both public surfaces: the HTTP API for clients and the TCP
	// listener used by Particle devices.
	if err := server.Start(context.Background()); err != nil {
		logger.Error("start server", "error", err)
		os.Exit(1)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown server", "error", err)
		os.Exit(1)
	}
}
