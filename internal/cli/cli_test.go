package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tack/internal/cli"
	"tack/internal/testutil"
)

func TestInitCreatesRepoState(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	var stdout, stderr bytes.Buffer
	if err := cli.Execute(context.Background(), []string{"init", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("init failed: %v stderr=%s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(repo, ".tack", "issues.db")); err != nil {
		t.Fatalf("issues.db missing: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, ".tack", "config.json")); err != nil {
		t.Fatalf("config.json missing: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(repo, ".tack", ".gitignore")); err != nil {
		t.Fatalf(".gitignore missing: %v", err)
	} else if string(got) != "*\n!.gitignore\n" {
		t.Fatalf("unexpected .tack/.gitignore: %q", string(got))
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	if payload["repo_root"] != wantRoot {
		t.Fatalf("unexpected repo root payload: %#v", payload)
	}
}

func TestInitPreservesExistingTackGitignore(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	tackDir := filepath.Join(repo, ".tack")
	if err := os.MkdirAll(tackDir, 0o755); err != nil {
		t.Fatalf("mkdir .tack: %v", err)
	}

	gitignorePath := filepath.Join(tackDir, ".gitignore")

	const customContent = "custom\n"
	if err := os.WriteFile(gitignorePath, []byte(customContent), 0o644); err != nil {
		t.Fatalf("write custom gitignore: %v", err)
	}

	runCLI(t, repo, "init")

	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .tack/.gitignore: %v", err)
	}

	if string(got) != customContent {
		t.Fatalf("expected existing .tack/.gitignore to be preserved, got %q", string(got))
	}
}

func TestSkillInstallModes(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	repoInstall := runJSON[map[string]any](t, repo, "skill", "install", "--json")
	if repoInstall["mode"] != "repo" {
		t.Fatalf("unexpected repo install mode: %#v", repoInstall)
	}

	repoSkillDir := filepath.Join(repo, ".agents", "skills", "tack")
	repoTarget := filepath.Join(repo, ".agents", "skills", "tack", "SKILL.md")
	assertFileHasContent(t, repoTarget)

	if got := repoInstall["installed_skill_dir"].(string); canonicalPath(t, got) != canonicalPath(t, repoSkillDir) {
		t.Fatalf("unexpected repo installed skill dir: %#v", repoInstall)
	}

	if got := repoInstall["installed_path"].(string); canonicalPath(t, got) != canonicalPath(t, repoTarget) {
		t.Fatalf("unexpected repo installed path: %#v", repoInstall)
	}

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homeInstall := runJSON[map[string]any](t, repo, "skill", "install", "--home", "--json")
	if homeInstall["mode"] != "home" {
		t.Fatalf("unexpected home install mode: %#v", homeInstall)
	}

	homeSkillDir := filepath.Join(homeDir, ".agents", "skills", "tack")
	homeTarget := filepath.Join(homeDir, ".agents", "skills", "tack", "SKILL.md")
	assertFileHasContent(t, homeTarget)

	if got := homeInstall["installed_skill_dir"].(string); canonicalPath(t, got) != canonicalPath(t, homeSkillDir) {
		t.Fatalf("unexpected home installed skill dir: %#v", homeInstall)
	}

	if got := homeInstall["installed_path"].(string); canonicalPath(t, got) != canonicalPath(t, homeTarget) {
		t.Fatalf("unexpected home installed path: %#v", homeInstall)
	}

	customRoot := filepath.Join(t.TempDir(), "custom-skills")

	customInstall := runJSON[map[string]any](t, repo, "skill", "install", "--path", customRoot, "--json")
	if customInstall["mode"] != "path" {
		t.Fatalf("unexpected custom install mode: %#v", customInstall)
	}

	customSkillDir := filepath.Join(customRoot, "tack")
	customTarget := filepath.Join(customRoot, "tack", "SKILL.md")
	assertFileHasContent(t, customTarget)

	if got := customInstall["installed_skill_dir"].(string); canonicalPath(t, got) != canonicalPath(t, customSkillDir) {
		t.Fatalf("unexpected custom installed skill dir: %#v", customInstall)
	}

	if got := customInstall["installed_path"].(string); canonicalPath(t, got) != canonicalPath(t, customTarget) {
		t.Fatalf("unexpected custom installed path: %#v", customInstall)
	}
}

func TestSkillInstallRejectsConflictingTargets(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	err := runCLIError(t, repo, "skill", "install", "--home", "--path", filepath.Join(t.TempDir(), "skills"))
	if err == nil || !strings.Contains(err.Error(), "use only one of --home or --path") {
		t.Fatalf("expected conflicting target error, got %v", err)
	}
}

func TestCreateClaimReadyCloseAndCommentJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)
	t.Setenv("TACK_ACTOR", "alice")

	runCLI(t, repo, "init")

	blocker := createIssue(t, repo, []string{
		"create",
		"--title", "blocker",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--json",
	})
	blocked := createIssue(t, repo, []string{
		"create",
		"--title", "blocked",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--depends-on", blocker["id"].(string),
		"--json",
	})

	ready := runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 1 || ready[0]["id"] != blocker["id"] {
		t.Fatalf("unexpected ready set: %#v", ready)
	}

	runJSON[map[string]any](t, repo, "update", blocker["id"].(string), "--claim", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 0 {
		t.Fatalf("expected claimed blocker to leave ready queue, got %#v", ready)
	}

	t.Setenv("TACK_ACTOR", "bob")

	err := runCLIError(t, repo, "update", blocker["id"].(string), "--claim", "--json")
	if err == nil || !strings.Contains(err.Error(), "already claimed by alice") {
		t.Fatalf("expected claim conflict, got %v", err)
	}

	t.Setenv("TACK_ACTOR", "alice")
	runJSON[map[string]any](t, repo, "close", blocker["id"].(string), "--reason", "done", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 1 || ready[0]["id"] != blocked["id"] {
		t.Fatalf("expected blocked issue to become ready, got %#v", ready)
	}

	comment := runJSON[map[string]any](t, repo, "comment", "add", blocked["id"].(string), "--body", "implemented", "--json")
	if comment["issue_id"] != blocked["id"] || comment["body"] != "implemented" {
		t.Fatalf("unexpected comment payload: %#v", comment)
	}

	comments := runJSON[[]map[string]any](t, repo, "comment", "list", blocked["id"].(string), "--json")
	if len(comments) != 1 || comments[0]["body"] != "implemented" {
		t.Fatalf("unexpected comments payload: %#v", comments)
	}
}

func createIssue(t *testing.T, repo string, args []string) map[string]any {
	t.Helper()

	return runJSON[map[string]any](t, repo, args...)
}

func runJSON[T any](t *testing.T, repo string, args ...string) T {
	t.Helper()

	var zero T

	out, err := runCLIBytes(repo, args...)
	if err != nil {
		t.Fatalf("command %v failed: %v", args, err)
	}

	if err := json.Unmarshal(out, &zero); err != nil {
		t.Fatalf("unmarshal output %s: %v", string(out), err)
	}

	return zero
}

func runCLI(t *testing.T, repo string, args ...string) {
	t.Helper()

	if _, err := runCLIBytes(repo, args...); err != nil {
		t.Fatalf("command %v failed: %v", args, err)
	}
}

func runCLIError(t *testing.T, repo string, args ...string) error {
	t.Helper()

	_, err := runCLIBytes(repo, args...)

	return err
}

func runCLIBytes(repo string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	err := cli.Execute(context.Background(), args, &stdout, &stderr)
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), err
		}

		return stdout.Bytes(), err
	}

	return stdout.Bytes(), nil
}

func assertFileHasContent(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if !strings.Contains(string(data), "tack agent workflow") {
		t.Fatalf("unexpected skill contents in %s: %q", path, string(data))
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("filepath.Abs(%s): %v", path, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved
	}

	return abs
}
