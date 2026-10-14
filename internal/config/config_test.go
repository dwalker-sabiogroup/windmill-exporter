package config

import (
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct{ key, value string }{
		{"WINDMILL_URL", ""}, {"WINDMILL_URL", "https://secret@example.com"},
		{"WINDMILL_URL", "https://example.com?secret=x"}, {"WINDMILL_URL", "file:///tmp"},
		{"WINDMILL_URL", "https://example.com/api/"}, {"WINDMILL_URL", "https://example.com:99999"},
		{"WINDMILL_TOKEN", ""}, {"WINDMILL_TOKEN", "secret\n"},
		{"WINDMILL_COLLECTORS", "queue_status,queue_counts"}, {"WINDMILL_COLLECTORS", "queue_status,queue_status"},
		{"WINDMILL_COLLECTORS", "unknown"}, {"WINDMILL_TIMEOUT", "-1s"},
		{"EXPORTER_SCRAPE_TIMEOUT", "5s"}, {"EXPORTER_SCRAPE_TIMEOUT", "6m"},
		{"EXPORTER_DRAIN_DELAY", "-1s"}, {"EXPORTER_DRAIN_DELAY", "31s"},
		{"EXPORTER_SHUTDOWN_TIMEOUT", "0s"}, {"EXPORTER_SHUTDOWN_TIMEOUT", "15s"},
		{"EXPORTER_SHUTDOWN_TIMEOUT", "11m"},
		{"EXPORTER_PPROF_ENABLED", "invalid"},
		{"EXPORTER_LOG_LEVEL", "trace"}, {"EXPORTER_LISTEN_ADDRESS", "bad"},
		{"EXPORTER_LISTEN_ADDRESS", ":0"}, {"EXPORTER_MAX_RESPONSE_BYTES", "0"},
		{"WINDMILL_RUNNABLE_WINDOW", "30m"}, {"WINDMILL_RUNNABLE_WINDOW", "721h"},
		{"WINDMILL_RUNNABLE_WINDOW", "3600.5s"}, {"WINDMILL_RUNNABLE_LIMIT", "1001"},
		{"WINDMILL_RUNNABLE_ORDER_BY", "secret"},
		{"WINDMILL_WORKSPACES", "../other"}, {"WINDMILL_WORKSPACES", "test,test"},
		{"WINDMILL_WORKSPACES", "test,"}, {"WINDMILL_WORKER_LIMIT", "10001"},
		{"WINDMILL_WORKER_LIMIT", "0"}, {"WINDMILL_JOB_TAG_LIMIT", "1001"},
		{"WINDMILL_JOB_WINDOW", "25h"}, {"WINDMILL_JOB_WINDOW", "30s"},
		{"WINDMILL_JOB_WINDOW", "60.5s"}, {"WINDMILL_COLLECTORS", "job_outcomes"},
		{"WINDMILL_COLLECTORS", "workspace_queue"},
	}
	for _, test := range tests {
		t.Run(test.key+test.value, func(t *testing.T) {
			settings := map[string]string{"WINDMILL_URL": "https://example.com", "WINDMILL_TOKEN": "secret"}
			settings[test.key] = test.value
			_, err := Parse(func(key string) string { return settings[key] })
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestDefaultsAndPublicOnly(t *testing.T) {
	configuration, err := Parse(func(key string) string {
		switch key {
		case "WINDMILL_URL":
			return "http://localhost:8000/prefix/"
		case "WINDMILL_COLLECTORS":
			return "health_status"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Timeout != 5*time.Second || configuration.ScrapeTimeout != 10*time.Second || configuration.RunnableLimit != 100 || configuration.ServiceInstanceID == "" {
		t.Fatal("incorrect defaults")
	}
	if configuration.RunnableQuery().Get("window_secs") != "86400" {
		t.Fatal("incorrect window")
	}
	if configuration.DrainDelay != 5*time.Second || configuration.ShutdownTimeout != 20*time.Second {
		t.Fatal("incorrect shutdown defaults")
	}
	if configuration.PprofEnabled {
		t.Fatal("profiling must be disabled by default")
	}
}

func TestOperationalCollectors(t *testing.T) {
	configuration, err := Parse(func(key string) string {
		switch key {
		case "WINDMILL_URL":
			return "http://localhost:8000"
		case "WINDMILL_TOKEN":
			return "secret"
		case "WINDMILL_COLLECTORS":
			return "workers,job_outcomes,workspace_queue"
		case "WINDMILL_WORKSPACES":
			return "test, another"
		case "EXPORTER_PPROF_ENABLED":
			return "true"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(configuration.Workspaces) != 2 || configuration.Workspaces[1] != "another" || configuration.WorkerLimit != 1000 || configuration.JobWindow != time.Hour || configuration.JobTagLimit != 100 {
		t.Fatal("incorrect operational defaults")
	}
	if !configuration.PprofEnabled {
		t.Fatal("profiling enablement was ignored")
	}
}
