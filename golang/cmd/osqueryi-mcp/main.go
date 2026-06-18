package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var CommitHash = "dev"

const Version = "0.1.0"

type Config struct {
	BinaryPath string
	Timeout    time.Duration
	LockFile   string
	CacheFile  string
	Debug      bool
	LogFile    string
}

func loadConfig() Config {
	cfg := Config{
		BinaryPath: "osqueryi",
		Timeout:    30 * time.Second,
		LockFile:   "osqueryi-mcp.lock",
		CacheFile:  "osqueryi-mcp-cache.json",
		LogFile:    "osqueryi-mcp.log",
	}

	if val := os.Getenv("OSQUERYI_PATH"); val != "" {
		cfg.BinaryPath = val
	}
	if val := os.Getenv("OSQUERYI_TIMEOUT"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.Timeout = d
		}
	}
	if val := os.Getenv("OSQUERYI_LOCKFILE"); val != "" {
		cfg.LockFile = val
	}
	if val := os.Getenv("OSQUERYI_CACHEFILE"); val != "" {
		cfg.CacheFile = val
	}
	if val := os.Getenv("OSQUERYI_DEBUG"); val != "" {
		cfg.Debug = true
	}
	if val, ok := os.LookupEnv("OSQUERYI_LOGFILE"); ok {
		cfg.LogFile = val
	}

	return cfg
}

func acquireLock(lockFile string) (func(), error) {
	if lockFile == "off" {
		return func() {}, nil
	}

	f, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Read existing PID
	content, _ := io.ReadAll(f)
	if len(content) > 0 {
		pid, _ := strconv.Atoi(string(content))
		if pid > 0 {
			process, err := os.FindProcess(pid)
			if err == nil {
				// Signal 0 checks for process existence
				err = process.Signal(syscall.Signal(0))
				if err == nil {
					f.Close()
					return nil, fmt.Errorf("server already running (PID %d)", pid)
				}
			}
		}
	}

	// Write current PID
	_, _ = f.Seek(0, 0)
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d", os.Getpid()))

	return func() {
		f.Close()
		os.Remove(lockFile)
	}, nil
}

func setupLogging(cfg Config) *slog.Logger {
	var w io.Writer = os.Stderr
	if cfg.LogFile != "" && cfg.LogFile != "off" {
		f, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			w = f
		}
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func main() {
	showVersion := flag.Bool("v", false, "show version")
	flag.BoolVar(showVersion, "version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("osqueryi-mcp %s (%s)\n", Version, CommitHash)
		return
	}

	cfg := loadConfig()
	logger := setupLogging(cfg)

	// Validate binary
	path, err := exec.LookPath(cfg.BinaryPath)
	if err != nil {
		logger.Error("osqueryi not found", "path", cfg.BinaryPath, "error", err)
		os.Exit(1)
	}
	cfg.BinaryPath = path

	// Acquire lock
	release, err := acquireLock(cfg.LockFile)
	if err != nil {
		logger.Error("failed to acquire lock", "error", err)
		os.Exit(1)
	}
	defer release()

	executor := NewExecutor(cfg.BinaryPath, cfg.Timeout, cfg.CacheFile)

	cwd, _ := os.Getwd()
	logger.Info("server_environment", "cwd", cwd, "cache_file", cfg.CacheFile, "commit", CommitHash)

	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "osqueryi-mcp",
			Version: Version + "-" + CommitHash,
		},
		nil,
	)

	registerTools(s, executor)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Warm cache in background
	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeout*2)
		defer cancel()

		// If we already have all schemas (e.g. from disk cache), this will be a no-op
		if err := executor.ensureAllSchemas(warmCtx); err != nil {
			logger.Warn("failed to warm cache", "error", err)
		} else {
			logger.Info("cache warmed")
		}
	}()

	logger.Info("starting osqueryi-mcp server", "bin", cfg.BinaryPath, "commit", CommitHash)
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
