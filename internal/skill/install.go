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
	agentsDir := filepath.Dir(skillsRoot)
	if filepath.Base(agentsDir) == ".agents" {
		createdAgentsDir, err := ensureDir(agentsDir)
		if err != nil {
			return InstallResult{}, err
		}

		if createdAgentsDir {
			err = writeFileIfMissing(filepath.Join(agentsDir, ".gitignore"), []byte(ignoreAllContents), 0o644)
			if err != nil {
				return InstallResult{}, err
			}
		}
	}

	targetDir := filepath.Join(skillsRoot, TackSkillName)

	err := os.MkdirAll(targetDir, 0o755)
	if err != nil {
		return InstallResult{}, err
	}

	targetPath := filepath.Join(targetDir, tackSkillFileName)

	err = writeFileIfMissing(targetPath, []byte(TackSkillContent), 0o644)
	if err != nil {
		return InstallResult{}, err
	}

	return InstallResult{
		SkillsRoot:    skillsRoot,
		InstalledDir:  targetDir,
		InstalledPath: targetPath,
	}, nil
}

func ensureDir(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return false, nil
	case !os.IsNotExist(err):
		return false, err
	}

	err = os.MkdirAll(path, 0o755)
	if err != nil {
		return false, err
	}

	return true, nil
}

func writeFileIfMissing(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err == nil {
		_, err = file.Write(contents)
		closeErr := file.Close()

		if err != nil {
			return err
		}

		return closeErr
	}

	if os.IsExist(err) {
		return nil
	}

	return err
}
