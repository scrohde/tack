package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tack/internal/store"
)

func TempRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	if err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	return dir
}

func InitStore(t *testing.T, repoRoot string) *store.Store {
	t.Helper()

	err := store.InitRepo(repoRoot)
	if err != nil {
		t.Fatalf("store.InitRepo: %v", err)
	}

	s, err := store.Open(filepath.Join(repoRoot, ".tack", "issues.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		closeStore(s)
	})

	return s
}

func Chdir(t *testing.T, dir string) {
	t.Helper()

	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	err = os.Chdir(dir)
	if err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}

	t.Cleanup(func() {
		restoreChdir(old)
	})
}

func Context(t *testing.T) context.Context {
	t.Helper()

	return context.Background()
}

func closeStore(s *store.Store) {
	err := s.Close()
	if err != nil {
		return
	}
}

func restoreChdir(dir string) {
	err := os.Chdir(dir)
	if err != nil {
		return
	}
}
