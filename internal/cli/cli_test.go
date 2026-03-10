package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"tack/internal/cli"
	"tack/internal/testutil"
)

func TestInitCreatesRepoState(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	var stdout, stderr bytes.Buffer

	err := cli.Execute(context.Background(), []string{"init", "--json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("init failed: %v stderr=%s", err, stderr.String())
	}

	_, err = os.Stat(filepath.Join(repo, ".tack", "issues.db"))
	if err != nil {
		t.Fatalf("issues.db missing: %v", err)
	}

	_, err = os.Stat(filepath.Join(repo, ".tack", "config.json"))
	if err != nil {
		t.Fatalf("config.json missing: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(repo, ".tack", ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore missing: %v", err)
	}

	if string(got) != "*\n" {
		t.Fatalf("unexpected .tack/.gitignore: %q", string(got))
	}

	var payload map[string]any

	err = json.Unmarshal(stdout.Bytes(), &payload)
	if err != nil {
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

	err := os.MkdirAll(tackDir, 0o755)
	if err != nil {
		t.Fatalf("mkdir .tack: %v", err)
	}

	gitignorePath := filepath.Join(tackDir, ".gitignore")

	const customContent = "custom\n"

	err = os.WriteFile(gitignorePath, []byte(customContent), 0o644)
	if err != nil {
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
	repoSkillDir := filepath.Join(repo, ".agents", "skills", "tack")
	repoTarget := filepath.Join(repo, ".agents", "skills", "tack", "SKILL.md")
	repoGitignore := filepath.Join(repo, ".agents", ".gitignore")

	assertInstallJSON(t, repoInstall, "repo", filepath.Join(repo, ".agents", "skills"), repoSkillDir, repoTarget)
	assertFileHasContent(t, repoTarget)
	assertExactFileContent(t, repoGitignore, "*\n")

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homeInstall := runJSON[map[string]any](t, repo, "skill", "install", "--home", "--json")
	homeSkillDir := filepath.Join(homeDir, ".agents", "skills", "tack")
	homeTarget := filepath.Join(homeDir, ".agents", "skills", "tack", "SKILL.md")
	homeGitignore := filepath.Join(homeDir, ".agents", ".gitignore")

	assertInstallJSON(t, homeInstall, "home", filepath.Join(homeDir, ".agents", "skills"), homeSkillDir, homeTarget)
	assertFileHasContent(t, homeTarget)
	assertExactFileContent(t, homeGitignore, "*\n")

	customRoot := filepath.Join(t.TempDir(), "custom-skills")

	customInstall := runJSON[map[string]any](t, repo, "skill", "install", "--path", customRoot, "--json")
	customSkillDir := filepath.Join(customRoot, "tack")
	customTarget := filepath.Join(customRoot, "tack", "SKILL.md")

	assertInstallJSON(t, customInstall, "path", customRoot, customSkillDir, customTarget)
	assertFileHasContent(t, customTarget)
}

func TestSkillInstallPlaintextOutput(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	out, err := runCLIBytes(repo, "skill", "install")
	if err != nil {
		t.Fatalf("skill install failed: %v", err)
	}

	wantPath := canonicalPath(t, filepath.Join(repo, ".agents", "skills", "tack", "SKILL.md"))

	wantLine := "installed tack skill to " + wantPath + "\n"
	if string(out) != wantLine {
		t.Fatalf("unexpected install output: got %q want %q", string(out), wantLine)
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
		"--depends-on", stringField(t, blocker, "id"),
		"--json",
	})

	ready := runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 1 || ready[0]["id"] != blocker["id"] {
		t.Fatalf("unexpected ready set: %#v", ready)
	}

	runJSON[map[string]any](t, repo, "update", stringField(t, blocker, "id"), "--claim", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 0 {
		t.Fatalf("expected claimed blocker to leave ready queue, got %#v", ready)
	}

	t.Setenv("TACK_ACTOR", "bob")

	err := runCLIError(t, repo, "update", stringField(t, blocker, "id"), "--claim", "--json")
	if err == nil || !strings.Contains(err.Error(), "already claimed by alice") {
		t.Fatalf("expected claim conflict, got %v", err)
	}

	t.Setenv("TACK_ACTOR", "alice")
	runJSON[map[string]any](t, repo, "close", stringField(t, blocker, "id"), "--reason", "done", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 1 || ready[0]["id"] != blocked["id"] {
		t.Fatalf("expected blocked issue to become ready, got %#v", ready)
	}

	comment := runJSON[map[string]any](t, repo, "comment", "add", stringField(t, blocked, "id"), "--body", "implemented", "--json")
	if comment["issue_id"] != blocked["id"] || comment["body"] != "implemented" {
		t.Fatalf("unexpected comment payload: %#v", comment)
	}

	comments := runJSON[[]map[string]any](t, repo, "comment", "list", stringField(t, blocked, "id"), "--json")
	if len(comments) != 1 || comments[0]["body"] != "implemented" {
		t.Fatalf("unexpected comments payload: %#v", comments)
	}
}

func TestListAndReadySummaryJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)
	t.Setenv("TACK_ACTOR", "alice")

	runCLI(t, repo, "init")

	parent := createIssue(t, repo, []string{
		"create",
		"--title", "parent",
		"--type", "task",
		"--priority", "high",
		"--description", "parent body",
		"--label", "Backend",
		"--label", "api",
		"--json",
	})
	parentID := stringField(t, parent, "id")

	childOpen := createIssue(t, repo, []string{
		"create",
		"--title", "child open",
		"--type", "task",
		"--priority", "medium",
		"--description", "child body",
		"--parent", parentID,
		"--json",
	})

	childClosed := createIssue(t, repo, []string{
		"create",
		"--title", "child closed",
		"--type", "task",
		"--priority", "medium",
		"--description", "child body",
		"--parent", parentID,
		"--json",
	})

	blocker := createIssue(t, repo, []string{
		"create",
		"--title", "blocker",
		"--type", "bug",
		"--priority", "urgent",
		"--description", "blocker body",
		"--json",
	})
	blockerID := stringField(t, blocker, "id")

	blocked := createIssue(t, repo, []string{
		"create",
		"--title", "blocked",
		"--type", "feature",
		"--priority", "medium",
		"--description", "blocked body",
		"--depends-on", blockerID,
		"--label", "ops",
		"--json",
	})
	blockedID := stringField(t, blocked, "id")

	runJSON[map[string]any](t, repo, "close", stringField(t, childClosed, "id"), "--reason", "done", "--json")

	fullList := runJSON[[]map[string]any](t, repo, "list", "--json")

	fullBlocked := summaryByID(t, fullList, blockedID)
	if fullBlocked["description"] != "blocked body" {
		t.Fatalf("expected full list json to keep description, got %#v", fullBlocked)
	}

	listSummary := runJSON[[]map[string]any](t, repo, "list", "--json", "--summary")
	blockedSummary := summaryByID(t, listSummary, blockedID)
	assertSummaryKeys(t, blockedSummary)

	if _, ok := blockedSummary["description"]; ok {
		t.Fatalf("summary should omit description: %#v", blockedSummary)
	}

	if got := stringSlice(t, blockedSummary["blocked_by"]); !slices.Equal(got, []string{blockerID}) {
		t.Fatalf("unexpected blocked_by: %#v", blockedSummary)
	}

	if got := stringSlice(t, blockedSummary["labels"]); !slices.Equal(got, []string{"ops"}) {
		t.Fatalf("unexpected labels: %#v", blockedSummary)
	}

	parentSummary := summaryByID(t, listSummary, parentID)
	if got := stringSlice(t, parentSummary["labels"]); !slices.Equal(got, []string{"api", "backend"}) {
		t.Fatalf("unexpected parent labels: %#v", parentSummary)
	}

	if got := stringSlice(t, parentSummary["open_children"]); !slices.Equal(got, []string{stringField(t, childOpen, "id")}) {
		t.Fatalf("unexpected open_children: %#v", parentSummary)
	}

	readySummary := runJSON[[]map[string]any](t, repo, "ready", "--json", "--summary")
	for _, item := range readySummary {
		assertSummaryKeys(t, item)

		if _, ok := item["description"]; ok {
			t.Fatalf("ready summary should omit description: %#v", item)
		}

		if item["id"] == blockedID {
			t.Fatalf("blocked issue should not appear in ready summary: %#v", readySummary)
		}
	}
}

func TestSummaryRequiresJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	runCLI(t, repo, "init")

	err := runCLIError(t, repo, "list", "--summary")
	if err == nil || !strings.Contains(err.Error(), "--summary requires --json") {
		t.Fatalf("expected summary/json usage error, got %v", err)
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

	err = json.Unmarshal(out, &zero)
	if err != nil {
		t.Fatalf("unmarshal output %s: %v", string(out), err)
	}

	return zero
}

func runCLI(t *testing.T, repo string, args ...string) {
	t.Helper()

	_, err := runCLIBytes(repo, args...)
	if err != nil {
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

func assertExactFileContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if string(data) != want {
		t.Fatalf("unexpected file contents in %s: got %q want %q", path, string(data), want)
	}
}

func stringField(t *testing.T, data map[string]any, key string) string {
	t.Helper()

	value, ok := data[key]
	if !ok {
		t.Fatalf("missing key %q in %#v", key, data)
	}

	text, ok := value.(string)
	if !ok {
		t.Fatalf("expected %q to be string, got %T", key, value)
	}

	return text
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

func summaryByID(t *testing.T, data []map[string]any, id string) map[string]any {
	t.Helper()

	for _, item := range data {
		if item["id"] == id {
			return item
		}
	}

	t.Fatalf("missing summary for %s in %#v", id, data)

	return nil
}

func assertSummaryKeys(t *testing.T, data map[string]any) {
	t.Helper()

	wantKeys := []string{
		"assignee",
		"blocked_by",
		"id",
		"labels",
		"open_children",
		"parent_id",
		"priority",
		"status",
		"title",
		"type",
	}

	gotKeys := make([]string, 0, len(data))
	for key := range data {
		gotKeys = append(gotKeys, key)
	}

	sort.Strings(gotKeys)

	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("unexpected summary json keys: got %v want %v", gotKeys, wantKeys)
	}
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected slice, got %T", value)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("expected string slice item, got %T", item)
		}

		out = append(out, text)
	}

	return out
}

func assertInstallJSON(t *testing.T, data map[string]any, mode, skillsRoot, skillDir, skillPath string) {
	t.Helper()

	wantKeys := []string{
		"installed_path",
		"installed_skill_dir",
		"mode",
		"skill_name",
		"skills_root",
	}

	gotKeys := make([]string, 0, len(data))
	for key := range data {
		gotKeys = append(gotKeys, key)
	}

	sort.Strings(gotKeys)

	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("unexpected install json keys: got %v want %v", gotKeys, wantKeys)
	}

	if data["mode"] != mode {
		t.Fatalf("unexpected install mode: %#v", data)
	}

	if data["skill_name"] != "tack" {
		t.Fatalf("unexpected skill name: %#v", data)
	}

	if got := stringField(t, data, "skills_root"); canonicalPath(t, got) != canonicalPath(t, skillsRoot) {
		t.Fatalf("unexpected skills root: %#v", data)
	}

	if got := stringField(t, data, "installed_skill_dir"); canonicalPath(t, got) != canonicalPath(t, skillDir) {
		t.Fatalf("unexpected installed skill dir: %#v", data)
	}

	if got := stringField(t, data, "installed_path"); canonicalPath(t, got) != canonicalPath(t, skillPath) {
		t.Fatalf("unexpected installed path: %#v", data)
	}
}
