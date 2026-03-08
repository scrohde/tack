package issues

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"tack/internal/config"
)

func ResolveActor(repoRoot, explicit string) (string, error) {
	if v := strings.TrimSpace(explicit); v != "" {
		return v, nil
	}

	if v := strings.TrimSpace(os.Getenv("TACK_ACTOR")); v != "" {
		return v, nil
	}

	cfg, err := config.Load(repoRoot)
	if err != nil {
		return "", err
	}

	if v := strings.TrimSpace(cfg.Actor); v != "" {
		return v, nil
	}

	cmd := exec.Command("git", "config", "user.name")
	cmd.Dir = repoRoot

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	err = cmd.Run()
	if err == nil {
		if v := strings.TrimSpace(stdout.String()); v != "" {
			return v, nil
		}
	}

	current, err := user.Current()
	if err == nil {
		if v := strings.TrimSpace(current.Username); v != "" {
			return v, nil
		}
	}

	return "", errors.New("unable to resolve actor; set --actor, TACK_ACTOR, or .tack/config.json")
}
