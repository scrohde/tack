package issues

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func EditBuffer(initial string) (string, error) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return "", fmt.Errorf("$EDITOR is not set")
	}

	file, err := os.CreateTemp("", "tack-edit-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)

	if _, err := file.WriteString(initial); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command("/bin/sh", "-c", "$EDITOR \"$1\"", "editor", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
