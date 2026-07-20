package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigEnvironmentOverrides(t *testing.T) {
	t.Setenv("OSQUERYI_PATH", "/tmp/custom-osqueryi")
	t.Setenv("OSQUERYI_TIMEOUT", "2s")
	t.Setenv("OSQUERYI_LOCKFILE", "off")
	t.Setenv("OSQUERYI_CACHEFILE", "cache.json")
	t.Setenv("OSQUERYI_LOGFILE", "")
	t.Setenv("OSQUERYI_DEBUG", "1")

	cfg := loadConfig()
	if cfg.BinaryPath != "/tmp/custom-osqueryi" || cfg.Timeout.String() != "2s" || cfg.LockFile != "off" || cfg.CacheFile != "cache.json" || cfg.LogFile != "" || !cfg.Debug {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestAcquireLockRejectsSecondServerAndCleansUp(t *testing.T) {
	lockFile := filepath.Join(t.TempDir(), "server.lock")
	release, err := acquireLock(lockFile)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if _, err := acquireLock(lockFile); err == nil {
		t.Fatal("acquire second lock succeeded")
	}

	release()
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after release: %v", err)
	}

	release, err = acquireLock(lockFile)
	if err != nil {
		t.Fatalf("acquire lock after release: %v", err)
	}
	release()
}
