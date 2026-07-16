package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	heimdal "github.com/coadan/heimdal"
)

const embeddedSkillRoot = "skills/heimdal-playwright-qa"

func defaultSkillDestination() string {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".codex", "skills", "heimdal-playwright-qa")
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "skills", "heimdal-playwright-qa")
}

func installSkill(destination string, force bool) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve skill destination: %w", err)
	}
	if info, statErr := os.Stat(destination); statErr == nil {
		if !force {
			return fmt.Errorf("skill destination already exists: %s (use --force to replace it)", destination)
		}
		if !info.IsDir() {
			return fmt.Errorf("skill destination is not a directory: %s", destination)
		}
	}
	temporary := destination + ".tmp"
	_ = os.RemoveAll(temporary)
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		return fmt.Errorf("create temporary skill directory: %w", err)
	}
	defer os.RemoveAll(temporary)
	err = fs.WalkDir(heimdal.SkillFiles, embeddedSkillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, embeddedSkillRoot)
		relative = strings.TrimPrefix(relative, "/")
		if relative == "" {
			return nil
		}
		target := filepath.Join(temporary, filepath.FromSlash(relative))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		contents, readErr := fs.ReadFile(heimdal.SkillFiles, path)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.MkdirAll(filepath.Dir(target), 0o755); writeErr != nil {
			return writeErr
		}
		return os.WriteFile(target, contents, 0o644)
	})
	if err != nil {
		return fmt.Errorf("materialize embedded skill: %w", err)
	}
	if force {
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("replace skill destination: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create skill parent: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("install skill: %w", err)
	}
	return nil
}
