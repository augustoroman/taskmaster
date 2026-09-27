// Package backup writes periodic snapshots of the database to a directory and
// prunes old ones.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	prefix    = "tasks-"
	suffix    = ".db"
	stampFmt  = "20060102T150405Z"
	tmpSuffix = ".tmp"
)

// Snapshotter writes a consistent copy of the database to path, which must not
// exist (store.DB.Backup).
type Snapshotter interface {
	Backup(ctx context.Context, path string) error
}

type Config struct {
	Dir      string
	Keep     int           // how many snapshots to keep
	Interval time.Duration // how often to take one
}

// Run takes a snapshot whenever the newest one is older than the interval,
// checking at startup and then every 10 minutes, until ctx is done.
func Run(ctx context.Context, db Snapshotter, cfg Config, now func() time.Time) {
	check := func() {
		if _, err := MaybeBackup(ctx, db, cfg, now()); err != nil && ctx.Err() == nil {
			slog.Error("backup", "err", err)
		}
	}
	check()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// MaybeBackup takes a snapshot if the newest is older than cfg.Interval and
// prunes to cfg.Keep. It returns the new snapshot's path, or "" if none was
// due.
func MaybeBackup(ctx context.Context, db Snapshotter, cfg Config, now time.Time) (string, error) {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return "", err
	}
	existing, err := list(cfg.Dir)
	if err != nil {
		return "", err
	}
	if len(existing) > 0 && now.Sub(existing[len(existing)-1].at) < cfg.Interval {
		return "", nil
	}
	path := filepath.Join(cfg.Dir, prefix+now.UTC().Format(stampFmt)+suffix)
	tmp := path + tmpSuffix
	os.Remove(tmp) // left over from an interrupted run
	if err := db.Backup(ctx, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	slog.Info("backup written", "path", path)
	existing, err = list(cfg.Dir)
	if err != nil {
		return path, err
	}
	for len(existing) > max(cfg.Keep, 1) {
		if err := os.Remove(existing[0].path); err != nil {
			return path, err
		}
		existing = existing[1:]
	}
	return path, nil
}

type snapshot struct {
	path string
	at   time.Time
}

// list returns the snapshots in dir, oldest first. Other files are ignored.
func list(dir string) ([]snapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []snapshot
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		at, err := time.Parse(stampFmt, strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix))
		if err != nil {
			continue
		}
		out = append(out, snapshot{filepath.Join(dir, name), at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out, nil
}
