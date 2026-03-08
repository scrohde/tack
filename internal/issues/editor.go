package issues

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

func EditBuffer(initial string) (string, error) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		return "", errors.New("$EDITOR is not set")
	}

	file, err := os.CreateTemp("", "tack-edit-*.txt")
	if err != nil {
		return "", err
	}

	path := file.Name()
	defer removeTempFile(path)

	_, err = file.WriteString(initial)
	if err != nil {
		closeFile(file)

		return "", err
	}

	err = file.Close()
	if err != nil {
		return "", err
	}

	cmd := exec.Command("/bin/sh", "-c", "$EDITOR \"$1\"", "editor", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = os.Environ()

	err = cmd.Run()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func removeTempFile(path string) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
}

func closeFile(file *os.File) {
	err := file.Close()
	if err != nil {
		return
	}
}
