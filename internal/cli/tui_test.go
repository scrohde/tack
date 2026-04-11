package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"tack/internal/testutil"
	"tack/internal/tui"
)

func TestTUIStartupFlagsSeedOptions(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	var (
		gotOptions tui.StartupOptions
		stdout     bytes.Buffer
		stderr     bytes.Buffer
	)

	previous := launchTUI
	launchTUI = func(_ context.Context, _, _ io.Writer, options tui.StartupOptions) error {
		gotOptions = options
		return nil
	}

	defer func() {
		launchTUI = previous
	}()

	err := Execute(context.Background(), []string{
		"tui",
		"--ready",
		"--status", " blocked ",
		"--type", " bug ",
		"--label", " api ",
		"--assignee", " alice ",
		"--limit", "7",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("tui failed: %v", err)
	}

	if gotOptions.Source != tui.DataSourceReady {
		t.Fatalf("unexpected data source: %#v", gotOptions)
	}

	if len(gotOptions.Filter.Statuses) != 1 || gotOptions.Filter.Statuses[0] != "blocked" ||
		len(gotOptions.Filter.Types) != 1 || gotOptions.Filter.Types[0] != "bug" ||
		len(gotOptions.Filter.Labels) != 1 || gotOptions.Filter.Labels[0] != "api" ||
		len(gotOptions.Filter.Assignees) != 1 || gotOptions.Filter.Assignees[0] != "alice" ||
		gotOptions.Filter.Limit != 7 {
		t.Fatalf("unexpected filters: %#v", gotOptions.Filter)
	}
}

func TestTUIUsesRepoInitializationErrors(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	var stdout, stderr bytes.Buffer

	err := Execute(context.Background(), []string{"tui"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "tack is not initialized") {
		t.Fatalf("expected init error, got %v", err)
	}
}
