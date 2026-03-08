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
	root := t.TempDir()

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
}
