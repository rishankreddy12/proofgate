package config

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Watcher polls the config file and calls onChange with each new valid config.
// Polling (not inotify) works with Kubernetes ConfigMap symlink swaps.
type Watcher struct {
	path     string
	interval time.Duration
	onChange func(*Config)
	mu       sync.Mutex
	lastSum  [32]byte
}

func NewWatcher(path string, interval time.Duration, onChange func(*Config)) *Watcher {
	w := &Watcher{path: path, interval: interval, onChange: onChange}
	if b, err := os.ReadFile(path); err == nil {
		w.lastSum = sha256.Sum256(b)
	}
	return w
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.check(false); err != nil {
				slog.Warn("config reload rejected, keeping previous config", "err", err)
			}
		}
	}
}

// Reload forces a reload even if the file is unchanged (used by POST /admin/reload).
func (w *Watcher) Reload() error { return w.check(true) }

func (w *Watcher) check(force bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := os.ReadFile(w.path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if sum == w.lastSum && !force {
		return nil
	}
	c, err := Parse(b)
	if err != nil {
		w.lastSum = sum // do not log the same bad file every tick
		return err
	}
	w.lastSum = sum
	w.onChange(c)
	slog.Info("config reloaded", "path", w.path)
	return nil
}
