package config

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestWatchConfigReloads(t *testing.T) {
	tmp := t.TempDir() + "/cfg.yaml"
	initial := "llm:\n  enabled: false\n"
	updated := "llm:\n  enabled: true\n"

	if err := os.WriteFile(tmp, []byte(initial), 0600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}
	SetRuntimeConfig(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.Default()
	if err := WatchConfig(ctx, tmp, logger); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Write updated file and wait for reload to propagate
	if err := os.WriteFile(tmp, []byte(updated), 0600); err != nil {
		t.Fatalf("write updated config: %v", err)
	}

	// Poll for up to 3s
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := GetRuntimeConfig()
		if got != nil && got.LLM.Enabled {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("watcher did not reload updated config")
}
