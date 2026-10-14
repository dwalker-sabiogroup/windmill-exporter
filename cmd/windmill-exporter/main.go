package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dwalker-sabiogroup/windmill-exporter/internal/config"
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/exporter"
	"github.com/dwalker-sabiogroup/windmill-exporter/internal/windmill"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("Exporter stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 1 {
		return fmt.Errorf("configuration is environment-only; command-line arguments are not supported")
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: configuration.LogLevel}))
	if configuration.URL.Scheme == "http" && configuration.Token != "" {
		logger.Warn("Bearer token will be sent over unencrypted HTTP; use HTTPS outside trusted networks")
	}
	client := windmill.New(configuration.URL, configuration.Token, configuration.Timeout, configuration.MaxResponseBytes)
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mux := http.NewServeMux()
	mux.Handle("/metrics", exporter.NewHandler(configuration, client, logger, version))
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("ok\n"))
	})
	server := &http.Server{
		Addr: configuration.ListenAddress, Handler: mux,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: configuration.ScrapeTimeout + 5*time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 * 1024,
		BaseContext:    func(_ net.Listener) context.Context { return ctx },
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("Exporter listening", "listen_address", server.Addr, "collectors", configuration.Collectors, "version", version)
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("Exporter shut down")
	return nil
}
