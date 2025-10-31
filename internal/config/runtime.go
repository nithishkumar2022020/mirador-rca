package config

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

var runtimeCfg atomic.Value // stores *Config

// SetRuntimeConfig stores the current config for runtime access.
func SetRuntimeConfig(cfg *Config) {
	runtimeCfg.Store(cfg)
}

// GetRuntimeConfig returns the latest runtime config or nil if not set.
func GetRuntimeConfig() *Config {
	v := runtimeCfg.Load()
	if v == nil {
		return nil
	}
	return v.(*Config)
}

// WatchConfig watches the provided config file and reloads it into the runtime store when it changes.
// This function returns an error if the watcher cannot be created or the path cannot be watched.
// The watcher goroutine will stop when ctx is canceled.
func WatchConfig(ctx context.Context, path string, logger *slog.Logger) error {
	if path == "" {
		return errors.New("empty config path")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(path); err != nil {
		watcher.Close()
		return err
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					logger.Info("config change detected, reloading", slog.String("path", ev.Name))
					newCfg, err := Load(path)
					if err != nil {
						logger.Error("failed to reload config", slog.Any("error", err))
						continue
					}
					SetRuntimeConfig(newCfg)
					logger.Info("config reloaded")
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("config watcher error", slog.Any("error", err))
			}
		}
	}()

	return nil
}
