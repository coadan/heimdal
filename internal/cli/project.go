package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Discover(start string) (Project, error) {
	root, err := projectRoot(start)
	if err != nil {
		return Project{}, err
	}
	playwrightConfig := hasPlaywrightConfig(root)
	cfg, configFile, err := loadConfig(root, playwrightConfig)
	if err != nil {
		return Project{}, err
	}
	if cfg.Playwright.Config != "" {
		configured := configRelativePath(root, cfg.Playwright.Config)
		if info, statErr := os.Stat(configured); statErr == nil && info.IsDir() {
			return Project{}, fmt.Errorf("Playwright config is a directory: %s", configured)
		}
		playwrightConfig = cfg.Playwright.Config
	}
	packageManager := detectPackageManager(root)
	runner, err := resolveRunner(root, packageManager, cfg.Playwright.Runner)
	if err != nil {
		return Project{}, err
	}
	agentRunner := resolveAgentRunner(root, packageManager, cfg.Session.Runner)
	branch := gitValue(root, "branch", "--show-current")
	if branch == "" {
		branch = gitValue(root, "rev-parse", "--short", "HEAD")
	}
	if branch == "" {
		branch = "detached"
	}
	return Project{
		Root:             root,
		Branch:           branch,
		Config:           cfg,
		ConfigFile:       configFile,
		PlaywrightConfig: playwrightConfig,
		PackageManager:   packageManager,
		Runner:           runner,
		AgentRunner:      agentRunner,
	}, nil
}

func projectRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	if info, statErr := os.Stat(absolute); statErr == nil && !info.IsDir() {
		absolute = filepath.Dir(absolute)
	}
	if root := gitValue(absolute, "rev-parse", "--show-toplevel"); root != "" {
		if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			return resolved, nil
		}
		return filepath.Clean(root), nil
	}
	for current := absolute; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, configFileName)); err == nil {
			return current, nil
		}
		if _, err := os.Stat(filepath.Join(current, "package.json")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return absolute, nil
}

func gitValue(root string, args ...string) string {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func detectPackageManager(root string) string {
	for _, candidate := range []struct {
		name  string
		files []string
	}{
		{name: "pnpm", files: []string{"pnpm-lock.yaml", "pnpm-workspace.yaml"}},
		{name: "yarn", files: []string{"yarn.lock"}},
		{name: "bun", files: []string{"bun.lockb", "bun.lock"}},
		{name: "npm", files: []string{"package-lock.json", "npm-shrinkwrap.json"}},
	} {
		for _, file := range candidate.files {
			if _, err := os.Stat(filepath.Join(root, file)); err == nil {
				return candidate.name
			}
		}
	}
	return "npm"
}

func resolveRunner(root, packageManager string, configured []string) ([]string, error) {
	if len(configured) > 0 {
		return append([]string(nil), configured...), nil
	}
	local := filepath.Join(root, "node_modules", ".bin", "playwright")
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return []string{local}, nil
	}
	switch packageManager {
	case "pnpm":
		return []string{"pnpm", "exec", "playwright"}, nil
	case "yarn":
		return []string{"yarn", "playwright"}, nil
	case "bun":
		return []string{"bunx", "--no-install", "playwright"}, nil
	case "npm":
		return []string{"npx", "--no-install", "playwright"}, nil
	default:
		return nil, fmt.Errorf("unsupported package manager %q", packageManager)
	}
}

func resolveAgentRunner(root, packageManager string, configured []string) []string {
	if len(configured) > 0 {
		return append([]string(nil), configured...)
	}
	local := filepath.Join(root, "node_modules", ".bin", "playwright-cli")
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return []string{local}
	}
	if path, err := exec.LookPath("playwright-cli"); err == nil {
		return []string{path}
	}
	// Keep a package-manager fallback so `heimdal install agent-cli` can add
	// the official package without requiring a project-specific configuration.
	switch packageManager {
	case "pnpm":
		return []string{"pnpm", "exec", "playwright-cli"}
	case "yarn":
		return []string{"yarn", "playwright-cli"}
	case "bun":
		return []string{"bunx", "--no-install", "playwright-cli"}
	default:
		return []string{"npx", "--no-install", "playwright-cli"}
	}
}

func containsFlag(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func commandFor(project Project, subcommand string, forwarded []string) []string {
	command := append([]string(nil), project.Runner...)
	command = append(command, subcommand)
	if project.PlaywrightConfig != "" && !containsFlag(forwarded, "--config", "-c") {
		command = append(command, "--config", project.PlaywrightConfig)
	}
	return append(command, forwarded...)
}

func runCapture(root string, command []string, env []string) (string, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = root
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func baseEnvironment() []string { return os.Environ() }

func commandString(command []string) string {
	parts := make([]string, len(command))
	for i, part := range command {
		if strings.ContainsAny(part, " \t\n\"") {
			parts[i] = fmt.Sprintf("%q", part)
		} else {
			parts[i] = part
		}
	}
	return strings.Join(parts, " ")
}
