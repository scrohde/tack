package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	Actor string `json:"actor"`
}

func Path(repoRoot string) string {
	return filepath.Join(repoRoot, ".tack", "config.json")
}

func Default() Config {
	return Config{}
}

func Load(repoRoot string) (Config, error) {
	path := Path(repoRoot)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}

	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func WriteDefault(repoRoot string) error {
	path := Path(repoRoot)
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	data, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}
