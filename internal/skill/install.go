package skill

import (
	"os"
	"path/filepath"
)

const (
	TackSkillName     = "tack"
	tackSkillFileName = "SKILL.md"
	ignoreAllContents = "*\n"
)

type InstallResult struct {
	SkillsRoot    string
	InstalledDir  string
	InstalledPath string
}

func InstallTackSkill(skillsRoot string) (InstallResult, error) {
	err := ensureAgentsGitignore(filepath.Dir(skillsRoot))
	if err != nil {
		return InstallResult{}, err
	}

	targetDir := filepath.Join(skillsRoot, TackSkillName)

	err = os.MkdirAll(targetDir, 0o755)
	if err != nil {
		return InstallResult{}, err
	}

	targetPath := filepath.Join(targetDir, tackSkillFileName)

	err = os.WriteFile(targetPath, []byte(TackSkillContent), 0o644)
	if err != nil {
		return InstallResult{}, err
	}

	return InstallResult{
		SkillsRoot:    skillsRoot,
		InstalledDir:  targetDir,
		InstalledPath: targetPath,
	}, nil
}

func ensureAgentsGitignore(agentsDir string) error {
	err := os.MkdirAll(agentsDir, 0o755)
	if err != nil {
		return err
	}

	path := filepath.Join(agentsDir, ".gitignore")

	_, err = os.Stat(path)
	if err == nil {
		return nil
	}

	if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(path, []byte(ignoreAllContents), 0o644)
}
