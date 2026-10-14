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
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	probes := &exporter.Probes{}
	mux := http.NewServeMux()
	mux.Handle("/metrics", exporter.NewHandler(configuration, client, logger, version))
	probes.Register(mux)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/metrics" && ctx.Err() != nil {
			http.Error(writer, "exporter is draining", http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(writer, request)
	})
	server := &http.Server{
		Addr: configuration.ListenAddress, Handler: handler,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: configuration.ScrapeTimeout + 5*time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 * 1024,
		BaseContext:    func(_ net.Listener) context.Context { return requests },
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	probes.Start()
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
	probes.Drain()
	stop()
	logger.Info("Exporter draining", "drain_delay", configuration.DrainDelay.String(), "shutdown_timeout", configuration.ShutdownTimeout.String())
	shutdown, cancel := context.WithTimeout(context.Background(), configuration.ShutdownTimeout)
	defer cancel()
	if configuration.DrainDelay > 0 {
		timer := time.NewTimer(configuration.DrainDelay)
		select {
		case <-timer.C:
		case <-shutdown.Done():
		}
		timer.Stop()
	}
	if err := server.Shutdown(shutdown); err != nil {
		cancelRequests()
		_ = server.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("Exporter shut down")
	return nil
}
