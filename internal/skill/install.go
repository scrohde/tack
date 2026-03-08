package skill

import (
	"os"
	"path/filepath"
)

const (
	TackSkillName     = "tack"
	tackSkillFileName = "SKILL.md"
)

type InstallResult struct {
	SkillsRoot    string
	InstalledDir  string
	InstalledPath string
}

func InstallTackSkill(skillsRoot string) (InstallResult, error) {
	targetDir := filepath.Join(skillsRoot, TackSkillName)

	err := os.MkdirAll(targetDir, 0o755)
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
