package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestExporterHelper(t *testing.T) {
	if os.Getenv("EXPORTER_TEST_HELPER") != "1" {
		return
	}
	os.Args = os.Args[:1]
	main()
	os.Exit(0)
}

func TestExporterLifecycle(t *testing.T) {
	var upstreamCalls atomic.Int64
	inFlight := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/health/status" || request.Header.Get("Authorization") != "" {
			t.Error("incorrect public request")
		}
		if upstreamCalls.Add(1) == 2 {
			close(inFlight)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(writer, `{"status":"healthy","checked_at":"2026-10-14T00:00:00Z","database_healthy":true,"workers_alive":2}`)
	}))
	defer upstream.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestExporterHelper$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "WINDMILL_") && !strings.HasPrefix(entry, "EXPORTER_") && !strings.HasPrefix(entry, "OTEL_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "EXPORTER_TEST_HELPER=1", "WINDMILL_URL="+upstream.URL,
		"WINDMILL_COLLECTORS=health_status", "EXPORTER_LISTEN_ADDRESS="+address,
		"EXPORTER_LOG_LEVEL=debug", "WINDMILL_TOKEN=sensitive-test-token", "EXPORTER_DRAIN_DELAY=1s", "EXPORTER_SHUTDOWN_TIMEOUT=15s")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	ready := false
	for !ready {
		select {
		case <-deadline.C:
			t.Fatal("exporter did not become ready")
		case <-ticker.C:
			response, err := client.Get("http://" + address + "/healthz")
			if err != nil {
				continue
			}
			ready = response.StatusCode == 200
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}
	checkEndpoint := func(path string, status int) {
		t.Helper()
		response, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("%s: status=%d, want=%d", path, response.StatusCode, status)
		}
	}
	for _, path := range []string{"/healthz", "/readyz", "/livez"} {
		checkEndpoint(path, 200)
	}
	checkEndpoint("/startupz", 404)
	checkEndpoint("/debug/pprof/", 404)
	checkEndpoint("/debug/pprof/heap", 404)
	if upstreamCalls.Load() != 0 {
		t.Fatal("probes queried Windmill")
	}
	response, err := client.Get("http://" + address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "windmill_workers_alive 2") {
		t.Fatal("invalid metrics response")
	}
	client.CloseIdleConnections()
	scrapeDone := make(chan error, 1)
	go func() {
		response, err := client.Get("http://" + address + "/metrics")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			err = readErr
			if err == nil && (response.StatusCode != 200 || !strings.Contains(string(body), "windmill_exporter_scrape_success 1")) {
				err = fmt.Errorf("in-flight scrape did not complete successfully")
			}
		}
		scrapeDone <- err
	}()
	select {
	case <-inFlight:
	case <-time.After(3 * time.Second):
		t.Fatal("scrape did not reach Windmill")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	drainDeadline := time.NewTimer(3 * time.Second)
	defer drainDeadline.Stop()
	draining := false
	for !draining {
		select {
		case <-drainDeadline.C:
			t.Fatal("readiness did not fail during shutdown")
		case <-ticker.C:
			response, err := client.Get("http://" + address + "/readyz")
			if err != nil {
				continue
			}
			draining = response.StatusCode == 503
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}
	checkEndpoint("/livez", 200)
	checkEndpoint("/healthz", 200)
	checkEndpoint("/metrics", 503)
	release <- struct{}{}
	select {
	case err := <-scrapeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight scrape was not drained")
	}
	client.CloseIdleConnections()
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("shutdown: %v\n%s", err, output.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("shutdown timed out")
	}
	if strings.Contains(output.String(), "sensitive-test-token") {
		t.Fatal("token leaked into logs")
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var log map[string]any
		if err := json.Unmarshal([]byte(line), &log); err != nil {
			t.Fatalf("non-JSON log: %s", line)
		}
		if log["level"] == nil || log["msg"] == nil {
			t.Fatal("log missing fields")
		}
	}
	if !strings.Contains(output.String(), `"level":"DEBUG"`) || !strings.Contains(output.String(), "Exporter shut down") {
		t.Fatal("missing debug/shutdown logs")
	}
}
