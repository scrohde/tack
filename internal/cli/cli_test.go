package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestHelpCommandMatchesFlagHelp(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	cases := []struct {
		name    string
		direct  []string
		viaHelp []string
		want    string
	}{
		{
			name:    "top level",
			direct:  []string{"--help"},
			viaHelp: []string{"help"},
			want:    "tack commands:",
		},
		{
			name:    "list",
			direct:  []string{"list", "--help"},
			viaHelp: []string{"help", "list"},
			want:    "usage: tack list [flags]",
		},
		{
			name:    "import",
			direct:  []string{"import", "--help"},
			viaHelp: []string{"help", "import"},
			want:    "usage: tack import --file <path> [--json]",
		},
		{
			name:    "show",
			direct:  []string{"show", "--help"},
			viaHelp: []string{"help", "show"},
			want:    "usage: tack show <id> [--json]",
		},
		{
			name:    "ready",
			direct:  []string{"ready", "--help"},
			viaHelp: []string{"help", "ready"},
			want:    "usage: tack ready [flags]",
		},
		{
			name:    "update",
			direct:  []string{"update", "--help"},
			viaHelp: []string{"help", "update"},
			want:    "usage: tack update <id> [flags]",
		},
		{
			name:    "comment group",
			direct:  []string{"comment", "--help"},
			viaHelp: []string{"help", "comment"},
			want:    "usage: tack comment add|list",
		},
		{
			name:    "comment add",
			direct:  []string{"comment", "add", "--help"},
			viaHelp: []string{"help", "comment", "add"},
			want:    "usage: tack comment add <id> [--body|--body-file]",
		},
		{
			name:    "dep group",
			direct:  []string{"dep", "--help"},
			viaHelp: []string{"help", "dep"},
			want:    "usage: tack dep add|remove|list",
		},
		{
			name:    "dep add",
			direct:  []string{"dep", "add", "--help"},
			viaHelp: []string{"help", "dep", "add"},
			want:    "usage: tack dep add <blocked-id> <blocker-id>",
		},
		{
			name:    "labels group",
			direct:  []string{"labels", "--help"},
			viaHelp: []string{"help", "labels"},
			want:    "usage: tack labels add|remove|list",
		},
		{
			name:    "labels add",
			direct:  []string{"labels", "add", "--help"},
			viaHelp: []string{"help", "labels", "add"},
			want:    "usage: tack labels add <id> <label> [label...]",
		},
		{
			name:    "skill install",
			direct:  []string{"skill", "install", "--help"},
			viaHelp: []string{"help", "skill", "install"},
			want:    "usage: tack skill install [--home|--path <dir>] [--json]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directOut, err := runCLIBytes(repo, tc.direct...)
			if err != nil {
				t.Fatalf("direct help %v failed: %v", tc.direct, err)
			}

			helpOut, err := runCLIBytes(repo, tc.viaHelp...)
			if err != nil {
				t.Fatalf("help route %v failed: %v", tc.viaHelp, err)
			}

			if string(directOut) != string(helpOut) {
				t.Fatalf("mismatched help output\ndirect:\n%s\nhelp:\n%s", string(directOut), string(helpOut))
			}

			if !strings.Contains(string(directOut), tc.want) {
				t.Fatalf("expected help output to contain %q, got %q", tc.want, string(directOut))
			}
		})
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

func TestReadyExcludesParentsWithOpenChildrenJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)
	t.Setenv("TACK_ACTOR", "alice")

	runCLI(t, repo, "init")

	parent := createIssue(t, repo, []string{
		"create",
		"--title", "parent",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--json",
	})
	child := createIssue(t, repo, []string{
		"create",
		"--title", "child",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--parent", stringField(t, parent, "id"),
		"--json",
	})
	standalone := createIssue(t, repo, []string{
		"create",
		"--title", "standalone",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--json",
	})

	ready := runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 2 || ready[0]["id"] != child["id"] || ready[1]["id"] != standalone["id"] {
		t.Fatalf("unexpected ready set with open child: %#v", ready)
	}

	listed := runJSON[[]map[string]any](t, repo, "list", "--json")
	if len(listed) != 3 || listed[0]["id"] != parent["id"] {
		t.Fatalf("expected parent to remain visible in list output, got %#v", listed)
	}

	shown := runJSON[map[string]any](t, repo, "show", stringField(t, parent, "id"), "--json")
	if shown["id"] != parent["id"] {
		t.Fatalf("expected parent to remain visible in show output, got %#v", shown)
	}

	exported := runJSON[map[string]any](t, repo, "export", "--json")

	exportedIssues, ok := exported["issues"].([]any)
	if !ok || len(exportedIssues) != 3 {
		t.Fatalf("expected parent to remain visible in export output, got %#v", exported)
	}

	runJSON[map[string]any](t, repo, "close", stringField(t, child, "id"), "--reason", "done", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json")
	if len(ready) != 2 || ready[0]["id"] != parent["id"] || ready[1]["id"] != standalone["id"] {
		t.Fatalf("unexpected ready set after child close: %#v", ready)
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
		"--priority", "medium",
		"--description", "body",
		"--label", "backend",
		"--json",
	})
	child := createIssue(t, repo, []string{
		"create",
		"--title", "child",
		"--type", "task",
		"--priority", "medium",
		"--description", "body",
		"--parent", stringField(t, parent, "id"),
		"--json",
	})
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
		"--label", "backend",
		"--json",
	})

	listed := runJSON[[]map[string]any](t, repo, "list", "--json", "--summary")
	if len(listed) != 4 {
		t.Fatalf("unexpected summary list length: %#v", listed)
	}

	parentSummary := listed[0]
	if gotKeys := sortedKeys(parentSummary); strings.Join(gotKeys, ",") != "assignee,blocked_by,id,labels,open_children,parent_id,priority,status,title,type" {
		t.Fatalf("unexpected summary keys: %v", gotKeys)
	}

	if parentSummary["id"] != parent["id"] {
		t.Fatalf("unexpected parent summary ordering: %#v", listed)
	}

	if labels := anyStrings(t, parentSummary["labels"]); len(labels) != 1 || labels[0] != "backend" {
		t.Fatalf("unexpected parent summary labels: %#v", parentSummary)
	}

	if children := anyStrings(t, parentSummary["open_children"]); len(children) != 1 || children[0] != child["id"] {
		t.Fatalf("unexpected parent summary open children: %#v", parentSummary)
	}

	if blockedBy := anyStrings(t, parentSummary["blocked_by"]); len(blockedBy) != 0 {
		t.Fatalf("expected no blockers for parent summary: %#v", parentSummary)
	}

	blockedSummary := listed[3]
	if blockedSummary["id"] != blocked["id"] {
		t.Fatalf("unexpected blocked summary ordering: %#v", listed)
	}

	if blockedBy := anyStrings(t, blockedSummary["blocked_by"]); len(blockedBy) != 1 || blockedBy[0] != blocker["id"] {
		t.Fatalf("unexpected blocked summary blockers: %#v", blockedSummary)
	}

	ready := runJSON[[]map[string]any](t, repo, "ready", "--json", "--summary")
	if len(ready) != 2 || ready[0]["id"] != child["id"] || ready[1]["id"] != blocker["id"] {
		t.Fatalf("unexpected ready summary with open child: %#v", ready)
	}

	runJSON[map[string]any](t, repo, "close", stringField(t, child, "id"), "--reason", "done", "--json")

	ready = runJSON[[]map[string]any](t, repo, "ready", "--json", "--summary")
	if len(ready) != 2 || ready[0]["id"] != parent["id"] || ready[1]["id"] != blocker["id"] {
		t.Fatalf("unexpected ready summary after child close: %#v", ready)
	}
}

func TestSummaryRequiresJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	for _, args := range [][]string{
		{"list", "--summary"},
		{"ready", "--summary"},
	} {
		err := runCLIError(t, repo, args...)
		if err == nil || !strings.Contains(err.Error(), "--summary requires --json") {
			t.Fatalf("expected summary/json validation for %v, got %v", args, err)
		}
	}
}

func TestReadyRejectsAssigneeFilter(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)

	err := runCLIError(t, repo, "ready", "--assignee", "alice")
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -assignee") {
		t.Fatalf("expected ready assignee flag rejection, got %v", err)
	}
}

func TestImportJSON(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)
	t.Setenv("TACK_ACTOR", "alice")

	runCLI(t, repo, "init")

	manifestPath := writeManifest(t, repo, `{
  "issues": [
    {
      "id": "epic",
      "title": "Imported epic",
      "type": "epic",
      "priority": "high",
      "description": "parent"
    },
    {
      "id": "task",
      "title": "Imported task",
      "parent": "epic",
      "depends_on": ["bug"],
      "labels": ["backend"]
    },
    {
      "id": "bug",
      "title": "Imported bug",
      "type": "bug",
      "priority": "urgent"
    }
  ]
}`)

	result := runJSON[map[string]any](t, repo, "import", "--file", manifestPath, "--json")
	if gotKeys := sortedKeys(result); strings.Join(gotKeys, ",") != "alias_map,created_ids" {
		t.Fatalf("unexpected import json keys: %v", gotKeys)
	}

	createdIDs := anyStrings(t, result["created_ids"])
	if len(createdIDs) != 3 || createdIDs[0] != "tk-1" || createdIDs[2] != "tk-3" {
		t.Fatalf("unexpected created ids: %#v", result)
	}

	aliasMap, ok := result["alias_map"].(map[string]any)
	if !ok {
		t.Fatalf("expected alias_map object, got %#v", result["alias_map"])
	}

	if aliasMap["task"] != "tk-2" {
		t.Fatalf("unexpected alias map: %#v", aliasMap)
	}

	task := runJSON[map[string]any](t, repo, "show", "tk-2", "--json")
	if task["parent_id"] != "tk-1" || task["type"] != "task" || task["priority"] != "medium" {
		t.Fatalf("unexpected imported task payload: %#v", task)
	}

	deps := runJSON[map[string]any](t, repo, "dep", "list", "tk-2", "--json")

	blockedBy, ok := deps["blocked_by"].([]any)
	if !ok || len(blockedBy) != 1 {
		t.Fatalf("unexpected dependency payload: %#v", deps)
	}

	blocker, ok := blockedBy[0].(map[string]any)
	if !ok || blocker["source_id"] != "tk-3" {
		t.Fatalf("unexpected blocker payload: %#v", deps)
	}
}

func TestImportPlaintextOutput(t *testing.T) {
	repo := testutil.TempRepo(t)
	testutil.Chdir(t, repo)
	t.Setenv("TACK_ACTOR", "alice")

	runCLI(t, repo, "init")

	manifestPath := writeManifest(t, repo, `{
  "issues": [
    {"id": "first", "title": "First"},
    {"id": "second", "title": "Second"}
  ]
}`)

	out, err := runCLIBytes(repo, "import", "--file", manifestPath)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	if string(out) != "imported 2 issues\nfirst\ttk-1\nsecond\ttk-2\n" {
		t.Fatalf("unexpected import output: %q", string(out))
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

func sortedKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func anyStrings(t *testing.T, value any) []string {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", value)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("expected string element, got %T", item)
		}

		out = append(out, text)
	}

	return out
}

func writeManifest(t *testing.T, repo, body string) string {
	t.Helper()

	path := filepath.Join(repo, "manifest.json")

	err := os.WriteFile(path, []byte(body), 0o644)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return path
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
