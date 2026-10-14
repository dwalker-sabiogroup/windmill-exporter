package config

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	URL                 *url.URL
	Token               string
	Collectors          []string
	Timeout             time.Duration
	ScrapeTimeout       time.Duration
	ListenAddress       string
	LogLevel            slog.Level
	MaxResponseBytes    int64
	RunnableWindow      time.Duration
	RunnableLimit       int
	RunnableWorkspace   string
	RunnableWorkerGroup string
	RunnableOrderBy     string
	Workspaces          []string
	WorkerLimit         int
	JobWindow           time.Duration
	JobTagLimit         int
	ServiceName         string
	ServiceNamespace    string
	ServiceInstanceID   string
	Environment         string
}

func Load() (Config, error) { return Parse(os.Getenv) }

var workspacePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func Parse(getenv func(string) string) (Config, error) {
	value := func(key, fallback string) string {
		if result := getenv(key); result != "" {
			return result
		}
		return fallback
	}
	configuration := Config{
		Token:               getenv("WINDMILL_TOKEN"),
		ListenAddress:       value("EXPORTER_LISTEN_ADDRESS", ":9108"),
		RunnableWorkspace:   getenv("WINDMILL_RUNNABLE_WORKSPACE"),
		RunnableWorkerGroup: getenv("WINDMILL_RUNNABLE_WORKER_GROUP"),
		RunnableOrderBy:     value("WINDMILL_RUNNABLE_ORDER_BY", "total_duration"),
		ServiceName:         value("OTEL_SERVICE_NAME", "windmill-exporter"),
		ServiceNamespace:    getenv("OTEL_SERVICE_NAMESPACE"),
		ServiceInstanceID:   getenv("OTEL_SERVICE_INSTANCE_ID"),
		Environment:         getenv("OTEL_DEPLOYMENT_ENVIRONMENT_NAME"),
	}
	var err error
	configuration.URL, err = url.Parse(getenv("WINDMILL_URL"))
	if err != nil || configuration.URL.Hostname() == "" || (configuration.URL.Scheme != "http" && configuration.URL.Scheme != "https") || configuration.URL.User != nil || configuration.URL.RawQuery != "" || configuration.URL.ForceQuery || configuration.URL.Fragment != "" {
		return Config{}, fmt.Errorf("WINDMILL_URL must be an HTTP(S) instance root without credentials, query or fragment")
	}
	if strings.HasSuffix(strings.TrimRight(configuration.URL.Path, "/"), "/api") {
		return Config{}, fmt.Errorf("WINDMILL_URL must be the instance root, not the /api URL")
	}
	if port := configuration.URL.Port(); port != "" {
		if number, parseErr := strconv.Atoi(port); parseErr != nil || number < 1 || number > 65535 {
			return Config{}, fmt.Errorf("WINDMILL_URL has an invalid port")
		}
	}
	_, port, err := net.SplitHostPort(configuration.ListenAddress)
	if err != nil {
		return Config{}, fmt.Errorf("EXPORTER_LISTEN_ADDRESS must be host:port")
	}
	if number, parseErr := strconv.Atoi(port); parseErr != nil || number < 1 || number > 65535 {
		return Config{}, fmt.Errorf("EXPORTER_LISTEN_ADDRESS requires a port between 1 and 65535")
	}
	if strings.ContainsAny(configuration.Token, "\r\n\t ") {
		return Config{}, fmt.Errorf("WINDMILL_TOKEN must not contain whitespace")
	}
	if raw := getenv("WINDMILL_WORKSPACES"); raw != "" {
		workspaces := map[string]bool{}
		for _, workspace := range strings.Split(raw, ",") {
			workspace = strings.TrimSpace(workspace)
			if !workspacePattern.MatchString(workspace) || workspaces[workspace] || len(workspaces) >= 20 {
				return Config{}, fmt.Errorf("WINDMILL_WORKSPACES requires at most 20 unique workspace IDs using letters, digits, underscores or hyphens (1-64 characters)")
			}
			workspaces[workspace] = true
			configuration.Workspaces = append(configuration.Workspaces, workspace)
		}
	}
	seen := map[string]bool{}
	for _, collector := range strings.Split(value("WINDMILL_COLLECTORS", "health_status,health_detailed,queue_status"), ",") {
		collector = strings.TrimSpace(collector)
		switch collector {
		case "health_status", "health_detailed", "queue_status", "queue_counts", "runnable_stats", "workers", "job_outcomes", "workspace_queue":
		default:
			return Config{}, fmt.Errorf("WINDMILL_COLLECTORS contains an unsupported collector")
		}
		if seen[collector] {
			return Config{}, fmt.Errorf("WINDMILL_COLLECTORS contains duplicate collectors")
		}
		if collector != "health_status" && configuration.Token == "" {
			return Config{}, fmt.Errorf("WINDMILL_TOKEN is required for authenticated collectors")
		}
		seen[collector] = true
		configuration.Collectors = append(configuration.Collectors, collector)
	}
	if seen["queue_status"] && seen["queue_counts"] {
		return Config{}, fmt.Errorf("choose queue_status or queue_counts, not both")
	}
	if (seen["job_outcomes"] || seen["workspace_queue"]) && len(configuration.Workspaces) == 0 {
		return Config{}, fmt.Errorf("WINDMILL_WORKSPACES is required for job_outcomes and workspace_queue")
	}
	for _, setting := range []struct {
		key, fallback string
		destination   *time.Duration
	}{
		{"WINDMILL_TIMEOUT", "5s", &configuration.Timeout},
		{"EXPORTER_SCRAPE_TIMEOUT", "10s", &configuration.ScrapeTimeout},
		{"WINDMILL_RUNNABLE_WINDOW", "24h", &configuration.RunnableWindow},
		{"WINDMILL_JOB_WINDOW", "1h", &configuration.JobWindow},
	} {
		*setting.destination, err = time.ParseDuration(value(setting.key, setting.fallback))
		if err != nil || *setting.destination <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", setting.key)
		}
	}
	if configuration.ScrapeTimeout <= configuration.Timeout || configuration.ScrapeTimeout > 5*time.Minute {
		return Config{}, fmt.Errorf("EXPORTER_SCRAPE_TIMEOUT must exceed WINDMILL_TIMEOUT and be at most 5m")
	}
	if configuration.RunnableWindow < time.Hour || configuration.RunnableWindow > 30*24*time.Hour || configuration.RunnableWindow%time.Second != 0 {
		return Config{}, fmt.Errorf("WINDMILL_RUNNABLE_WINDOW must be whole seconds between 1h and 720h")
	}
	if configuration.JobWindow < time.Minute || configuration.JobWindow > 24*time.Hour || configuration.JobWindow%time.Second != 0 {
		return Config{}, fmt.Errorf("WINDMILL_JOB_WINDOW must be whole seconds between 1m and 24h")
	}
	configuration.MaxResponseBytes, err = strconv.ParseInt(value("EXPORTER_MAX_RESPONSE_BYTES", "4194304"), 10, 64)
	if err != nil || configuration.MaxResponseBytes < 1 || configuration.MaxResponseBytes > 64*1024*1024 {
		return Config{}, fmt.Errorf("EXPORTER_MAX_RESPONSE_BYTES must be between 1 and 67108864")
	}
	configuration.RunnableLimit, err = strconv.Atoi(value("WINDMILL_RUNNABLE_LIMIT", "100"))
	if err != nil || configuration.RunnableLimit < 1 || configuration.RunnableLimit > 1000 {
		return Config{}, fmt.Errorf("WINDMILL_RUNNABLE_LIMIT must be between 1 and 1000")
	}
	for _, setting := range []struct {
		key, fallback string
		maximum       int
		destination   *int
	}{
		{"WINDMILL_WORKER_LIMIT", "1000", 10000, &configuration.WorkerLimit},
		{"WINDMILL_JOB_TAG_LIMIT", "100", 1000, &configuration.JobTagLimit},
	} {
		*setting.destination, err = strconv.Atoi(value(setting.key, setting.fallback))
		if err != nil || *setting.destination < 1 || *setting.destination > setting.maximum {
			return Config{}, fmt.Errorf("%s must be between 1 and %d", setting.key, setting.maximum)
		}
	}
	switch configuration.RunnableOrderBy {
	case "total_duration", "max_memory", "cpu":
	default:
		return Config{}, fmt.Errorf("WINDMILL_RUNNABLE_ORDER_BY must be total_duration, max_memory or cpu")
	}
	switch value("EXPORTER_LOG_LEVEL", "info") {
	case "debug":
		configuration.LogLevel = slog.LevelDebug
	case "info":
		configuration.LogLevel = slog.LevelInfo
	case "warn":
		configuration.LogLevel = slog.LevelWarn
	case "error":
		configuration.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("EXPORTER_LOG_LEVEL must be debug, info, warn or error")
	}
	if configuration.ServiceInstanceID == "" {
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			return Config{}, fmt.Errorf("generate service instance identity: %w", err)
		}
		identifier[6] = (identifier[6] & 0x0f) | 0x40
		identifier[8] = (identifier[8] & 0x3f) | 0x80
		configuration.ServiceInstanceID = fmt.Sprintf("%x-%x-%x-%x-%x", identifier[:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:])
	}
	return configuration, nil
}

func (configuration Config) RunnableQuery() url.Values {
	query := url.Values{
		"window_secs": {strconv.FormatInt(int64(configuration.RunnableWindow/time.Second), 10)},
		"limit":       {strconv.Itoa(configuration.RunnableLimit)},
		"order_by":    {configuration.RunnableOrderBy},
	}
	if configuration.RunnableWorkspace != "" {
		query.Set("workspace", configuration.RunnableWorkspace)
	}
	if configuration.RunnableWorkerGroup != "" {
		query.Set("worker_group", configuration.RunnableWorkerGroup)
	}
	return query
}
