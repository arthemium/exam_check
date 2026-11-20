package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Config struct {
	AppEnv               string
	ListenAddr           string
	DBPath               string
	PublicBaseURL        string
	FrontendOrigins      []string
	CookieSecure         bool
	SessionDays          int
	LoginUsername        string
	LoginPassword        string
	UploadDir            string
	StaticDir            string
	MaxUploadBytes       int64
	MaxPageBytes         int64
	MaxPagesPerExam      int
	MaxExamBytes         int64
	MaxRequestBytes      int64
	MaxJobsPerUserPerDay int
	MaxActiveJobsPerUser int
	OpenAIKey            string
	OpenAIModel          string // Legacy model used only when creating or migrating prompt rows.
	OpenAITimeout        time.Duration
	WorkerPoll           time.Duration
	JobMaxAttempts       int
	JobLease             time.Duration
	EnableWorker         bool
}

const defaultListenAddr = ":8000"

func DefaultConfig() Config {
	return Config{
		AppEnv: "development", ListenAddr: defaultListenAddr, DBPath: "data/db/examcheck.db",
		PublicBaseURL: "http://localhost:8000", SessionDays: 7, UploadDir: "data/uploads", StaticDir: "static",
		MaxUploadBytes: 100 << 20, MaxPageBytes: 10 << 20, MaxPagesPerExam: 10, MaxExamBytes: 20 << 20, MaxRequestBytes: 120 << 20,
		MaxJobsPerUserPerDay: 50, MaxActiveJobsPerUser: 2, OpenAITimeout: 90 * time.Second,
		WorkerPoll: 2 * time.Second, JobMaxAttempts: 3, JobLease: 300 * time.Second, EnableWorker: true,
	}
}

// ListenAddress resolves the server binding for both startup and health checks.
func ListenAddress() (string, error) {
	if address := os.Getenv("LISTEN_ADDR"); address != "" {
		return address, nil
	}
	if value := os.Getenv("PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
		return ":" + strconv.Itoa(port), nil
	}
	return defaultListenAddr, nil
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	loadStringSettings(&cfg)
	address, err := ListenAddress()
	if err != nil {
		return cfg, err
	}
	cfg.ListenAddr = address
	if err := loadNumberSettings(&cfg); err != nil {
		return cfg, err
	}
	for name, target := range map[string]*bool{
		"COOKIE_SECURE": &cfg.CookieSecure, "ENABLE_WORKER": &cfg.EnableWorker,
	} {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return cfg, fmt.Errorf("%s must be true or false", name)
			}
			*target = parsed
		}
	}
	if raw := os.Getenv("FRONTEND_ORIGINS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.FrontendOrigins); err != nil {
			return cfg, fmt.Errorf("FRONTEND_ORIGINS must be a JSON array of origins")
		}
	}
	err = cfg.Validate()
	return cfg, err
}

func loadStringSettings(cfg *Config) {
	settings := map[string]*string{
		"APP_ENV": &cfg.AppEnv, "DB_PATH": &cfg.DBPath,
		"PUBLIC_BASE_URL": &cfg.PublicBaseURL, "LOGIN_USERNAME": &cfg.LoginUsername,
		"LOGIN_PASSWORD": &cfg.LoginPassword, "UPLOAD_DIR": &cfg.UploadDir,
		"STATIC_DIR": &cfg.StaticDir, "OPENAI_API_KEY": &cfg.OpenAIKey, "OPENAI_MODEL": &cfg.OpenAIModel,
	}
	for name, target := range settings {
		if value, ok := os.LookupEnv(name); ok {
			*target = value
		}
	}
}

func loadNumberSettings(cfg *Config) error {
	integers := map[string]*int{
		"SESSION_DAYS": &cfg.SessionDays, "MAX_PAGES_PER_EXAM": &cfg.MaxPagesPerExam,
		"MAX_JOBS_PER_USER_PER_DAY": &cfg.MaxJobsPerUserPerDay,
		"MAX_ACTIVE_JOBS_PER_USER":  &cfg.MaxActiveJobsPerUser, "JOB_MAX_ATTEMPTS": &cfg.JobMaxAttempts,
	}
	for name, target := range integers {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			*target = parsed
		}
	}
	for name, target := range map[string]*int64{
		"MAX_UPLOAD_BYTES": &cfg.MaxUploadBytes,
		"MAX_PAGE_BYTES":   &cfg.MaxPageBytes, "MAX_EXAM_BYTES": &cfg.MaxExamBytes, "MAX_REQUEST_BYTES": &cfg.MaxRequestBytes,
	} {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			*target = parsed
		}
	}
	for name, target := range map[string]*time.Duration{
		"OPENAI_TIMEOUT_SECONDS": &cfg.OpenAITimeout, "WORKER_POLL_SECONDS": &cfg.WorkerPoll, "JOB_LEASE_SECONDS": &cfg.JobLease,
	} {
		if value, ok := os.LookupEnv(name); ok {
			parsed, err := time.ParseDuration(value + "s")
			if err != nil {
				return fmt.Errorf("%s must be a number of seconds", name)
			}
			*target = parsed
		}
	}
	return nil
}

func validOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || strings.HasSuffix(parsed.Host, ":") {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if strings.ContainsAny(value, "?#") || strings.ContainsFunc(value, unicode.IsSpace) {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func (cfg *Config) Validate() error {
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	if !validOrigin(cfg.PublicBaseURL) {
		return fmt.Errorf("PUBLIC_BASE_URL must be an HTTP(S) origin")
	}
	for i, value := range cfg.FrontendOrigins {
		cfg.FrontendOrigins[i] = strings.TrimRight(value, "/")
		if !validOrigin(cfg.FrontendOrigins[i]) {
			return fmt.Errorf("FRONTEND_ORIGINS contains an invalid origin")
		}
	}
	if cfg.AppEnv != "development" && cfg.AppEnv != "test" && cfg.AppEnv != "production" {
		return fmt.Errorf("invalid APP_ENV")
	}
	if cfg.AppEnv == "production" && (!cfg.CookieSecure || !strings.HasPrefix(cfg.PublicBaseURL, "https://")) {
		return fmt.Errorf("production requires HTTPS and COOKIE_SECURE=true")
	}
	if cfg.DBPath == "" || cfg.DBPath == ":memory:" || cfg.UploadDir == "" {
		return fmt.Errorf("DB_PATH and UPLOAD_DIR must use persistent filesystem paths")
	}
	if utf8.RuneCountInString(cfg.LoginUsername) > 120 || utf8.RuneCountInString(cfg.LoginPassword) > 1024 {
		return fmt.Errorf("login credentials exceed their length limits")
	}
	return cfg.validateLimits()
}

func (cfg Config) validateLimits() error {
	if cfg.SessionDays < 1 || cfg.SessionDays > 30 || cfg.MaxPagesPerExam < 1 || cfg.MaxPagesPerExam > 20 {
		return fmt.Errorf("session or page limits are out of range")
	}
	if cfg.MaxUploadBytes < 1 || cfg.MaxUploadBytes > 1<<30 || cfg.MaxPageBytes < 1 || cfg.MaxExamBytes < 1 || cfg.MaxRequestBytes < 1 || cfg.MaxRequestBytes > 1<<30 {
		return fmt.Errorf("upload limits are out of range")
	}
	if cfg.MaxJobsPerUserPerDay < 1 || cfg.MaxActiveJobsPerUser < 1 || cfg.JobMaxAttempts < 1 || cfg.JobMaxAttempts > 5 {
		return fmt.Errorf("job limits are out of range")
	}
	if cfg.OpenAITimeout <= 0 || cfg.OpenAITimeout > 300*time.Second || cfg.WorkerPoll <= 0 || cfg.JobLease < 60*time.Second {
		return fmt.Errorf("worker timeouts are out of range")
	}
	return nil
}

func (cfg Config) allowsOrigin(origin string) bool {
	if origin == cfg.PublicBaseURL {
		return true
	}
	for _, allowed := range cfg.FrontendOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}
