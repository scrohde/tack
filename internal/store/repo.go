package store

import (
	"fmt"
	"os"
	"path/filepath"

	"tack/internal/config"
)

func FindRepoRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}

	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		gitPath := filepath.Join(current, ".git")

		_, err := os.Stat(gitPath)
		if err == nil {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no git repo found from %s", start)
		}

		current = parent
	}
}

func dbPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".tack", "issues.db")
}

func gitignorePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".tack", ".gitignore")
}

func InitRepo(repoRoot string) error {
	dir := filepath.Join(repoRoot, ".tack")

	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return err
	}

	err = ensureTackGitignore(repoRoot)
	if err != nil {
		return err
	}

	err = config.WriteDefault(repoRoot)
	if err != nil {
		return err
	}

	store, err := Open(dbPath(repoRoot))
	if err != nil {
		return err
	}

	return store.Close()
}

func ensureTackGitignore(repoRoot string) error {
	path := gitignorePath(repoRoot)

	_, err := os.Stat(path)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(path, []byte("*\n!.gitignore\n"), 0o644)
}

func EnsureInitialized(repoRoot string) error {
	_, err := os.Stat(dbPath(repoRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tack is not initialized in %s; run `tack init`", repoRoot)
		}

		return err
	}

	return nil
}
