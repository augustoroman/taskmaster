package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/augustoroman/taskmaster/internal/store"
)

func TestMaybeBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "live.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.Tx(ctx, func(tx *store.Tx) error {
		return tx.InsertUser(&store.User{Email: "a@example.com", CreatedAt: time.Now(), LastLoginAt: time.Now()})
	}))

	cfg := Config{Dir: filepath.Join(dir, "backups"), Keep: 3, Interval: 24 * time.Hour}
	t0 := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)

	path, err := MaybeBackup(ctx, db, cfg, t0)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cfg.Dir, "tasks-20261001T030000Z.db"), path)

	// The snapshot is a working database with the data in it.
	snap, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, snap.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.GetUserByEmail("a@example.com")
		return err
	}))
	snap.Close()

	// Not due again until the interval has passed.
	path, err = MaybeBackup(ctx, db, cfg, t0.Add(23*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, path)

	// Keeps only the newest three; ignores unrelated files.
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Dir, "notes.txt"), nil, 0o600))
	for day := 1; day <= 4; day++ {
		_, err := MaybeBackup(ctx, db, cfg, t0.Add(time.Duration(day)*24*time.Hour))
		require.NoError(t, err)
	}
	names := []string{}
	entries, _ := os.ReadDir(cfg.Dir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"notes.txt", "tasks-20261003T030000Z.db", "tasks-20261004T030000Z.db", "tasks-20261005T030000Z.db"}, names)
}
