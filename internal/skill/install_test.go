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

func TestInstallTackSkillPreservesExistingFile(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".agents", "skills")

	targetDir := filepath.Join(root, TackSkillName)

	err := os.MkdirAll(targetDir, 0o755)
	if err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}

	targetPath := filepath.Join(targetDir, tackSkillFileName)

	const existingContent = "old content"

	err = os.WriteFile(targetPath, []byte(existingContent), 0o644)
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

	if string(got) != existingContent {
		t.Fatalf("expected existing skill content to be preserved, got %q", string(got))
	}

	_, err = os.Stat(filepath.Join(parent, ".agents", ".gitignore"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected no .agents/.gitignore for existing .agents dir, got %v", err)
	}
}

func TestInstallTackSkillCreatesAgentsGitignoreWhenCreatingAgentsDir(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".agents", "skills")

	_, err := InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	agentsGitignore, err := os.ReadFile(filepath.Join(parent, ".agents", ".gitignore"))
	if err != nil {
		t.Fatalf("read .agents/.gitignore: %v", err)
	}

	if string(agentsGitignore) != "*\n" {
		t.Fatalf("unexpected .agents/.gitignore: %q", string(agentsGitignore))
	}
}

func TestInstallTackSkillPreservesExistingAgentsContents(t *testing.T) {
	parent := t.TempDir()
	agentsDir := filepath.Join(parent, ".agents")
	root := filepath.Join(agentsDir, "skills")

	err := os.MkdirAll(filepath.Join(agentsDir, "notes"), 0o755)
	if err != nil {
		t.Fatalf("mkdir existing agents content: %v", err)
	}

	const existingContent = "keep me\n"

	path := filepath.Join(agentsDir, "notes", "custom.txt")

	err = os.WriteFile(path, []byte(existingContent), 0o644)
	if err != nil {
		t.Fatalf("seed existing agents file: %v", err)
	}

	_, err = InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing agents file: %v", err)
	}

	if string(got) != existingContent {
		t.Fatalf("expected existing agents file to be preserved, got %q", string(got))
	}

	_, err = os.Stat(filepath.Join(agentsDir, ".gitignore"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected no .agents/.gitignore to be created for existing .agents dir, got %v", err)
	}
}

func TestInstallTackSkillPreservesExistingAgentsGitignore(t *testing.T) {
	parent := t.TempDir()
	agentsDir := filepath.Join(parent, ".agents")
	root := filepath.Join(agentsDir, "skills")

	err := os.MkdirAll(root, 0o755)
	if err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}

	const customContent = "custom\n"

	err = os.WriteFile(filepath.Join(agentsDir, ".gitignore"), []byte(customContent), 0o644)
	if err != nil {
		t.Fatalf("write .agents/.gitignore: %v", err)
	}

	_, err = InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(agentsDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .agents/.gitignore: %v", err)
	}

	if string(got) != customContent {
		t.Fatalf("expected existing .agents/.gitignore to be preserved, got %q", string(got))
	}
}

func TestInstallTackSkillCustomPathDoesNotCreateParentGitignore(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")

	_, err := InstallTackSkill(root)
	if err != nil {
		t.Fatalf("InstallTackSkill: %v", err)
	}

	_, err = os.Stat(filepath.Join(parent, ".gitignore"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected no parent .gitignore for custom path install, got %v", err)
	}
}
