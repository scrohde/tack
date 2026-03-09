package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTackSkillBundleMatchesSource(t *testing.T) {
	sourcePath := filepath.Join("..", "..", ".agents", "skills", "tack", "SKILL.md")

	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source skill: %v", err)
	}

	if TackSkillContent != string(source) {
		t.Fatalf("bundled skill content does not match %s", sourcePath)
	}
}

func TestInstallTackSkillOverwritesExistingFile(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")

	targetDir := filepath.Join(root, TackSkillName)

	err := os.MkdirAll(targetDir, 0o755)
	if err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}

	targetPath := filepath.Join(targetDir, tackSkillFileName)

	err = os.WriteFile(targetPath, []byte("old content"), 0o644)
	if err != nil {
		t.Fatalf("seed target file: %v", err)
	}

	result, err := InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	if result.SkillsRoot != root {
		t.Fatalf("unexpected skills root: %s", result.SkillsRoot)
	}

	if result.InstalledDir != targetDir {
		t.Fatalf("unexpected installed dir: %s", result.InstalledDir)
	}

	if result.InstalledPath != targetPath {
		t.Fatalf("unexpected installed path: %s", result.InstalledPath)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}

	if string(got) != TackSkillContent {
		t.Fatalf("unexpected installed content: %q", string(got))
	}

	agentsGitignore, err := os.ReadFile(filepath.Join(parent, ".gitignore"))
	if err != nil {
		t.Fatalf("read .agents/.gitignore: %v", err)
	}

	if string(agentsGitignore) != "*\n" {
		t.Fatalf("unexpected .agents/.gitignore: %q", string(agentsGitignore))
	}
}

func TestInstallTackSkillPreservesExistingAgentsGitignore(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")

	err := os.MkdirAll(root, 0o755)
	if err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}

	const customContent = "custom\n"

	err = os.WriteFile(filepath.Join(parent, ".gitignore"), []byte(customContent), 0o644)
	if err != nil {
		t.Fatalf("write .agents/.gitignore: %v", err)
	}

	_, err = InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(parent, ".gitignore"))
	if err != nil {
		t.Fatalf("read .agents/.gitignore: %v", err)
	}

	if string(got) != customContent {
		t.Fatalf("expected existing .agents/.gitignore to be preserved, got %q", string(got))
	}
}
