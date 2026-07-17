package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	configFileName     = ".heimdal.json"
	defaultArtifactDir = ".dev/heimdal"
)

type Config struct {
	Version    int              `json:"version"`
	Playwright PlaywrightConfig `json:"playwright"`
	Session    SessionConfig    `json:"session,omitempty"`
	Artifacts  ArtifactConfig   `json:"artifacts"`
}

type PlaywrightConfig struct {
	Config   string            `json:"config,omitempty"`
	Runner   []string          `json:"runner,omitempty"`
	RunIDEnv string            `json:"run_id_env,omitempty"`
	PortEnv  string            `json:"port_env,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// SessionConfig describes the project-specific part of an interactive
// browser session. The browser itself remains owned by Playwright's agent
// CLI; Heimdal only supplies the server command, URL, environment, and
// optional executable override.
type SessionConfig struct {
	Runner               []string             `json:"runner,omitempty"`
	Command              []string             `json:"command,omitempty"`
	URL                  string               `json:"url,omitempty"`
	RunIDEnv             string               `json:"run_id_env,omitempty"`
	PortEnv              string               `json:"port_env,omitempty"`
	Env                  map[string]string    `json:"env,omitempty"`
	Browser              string               `json:"browser,omitempty"`
	BrowserLaunchOptions BrowserLaunchOptions `json:"browser_launch_options,omitempty"`
	ServerTimeoutMS      int                  `json:"server_timeout_ms,omitempty"`
}

// BrowserLaunchOptions is the small project-owned subset of Playwright launch
// options that Heimdal needs to pass through to the official agent CLI.
type BrowserLaunchOptions struct {
	Args    []string `json:"args,omitempty"`
	Channel string   `json:"channel,omitempty"`
}

type ArtifactConfig struct {
	Directory string `json:"directory,omitempty"`
}

type Project struct {
	Root             string
	Branch           string
	Config           Config
	ConfigFile       string
	PlaywrightConfig string
	PackageManager   string
	Runner           []string
	AgentRunner      []string
}

func defaultConfig(playwrightConfig string) Config {
	return Config{
		Version: 1,
		Playwright: PlaywrightConfig{
			Config:   playwrightConfig,
			RunIDEnv: "HEIMDAL_RUN_ID",
			PortEnv:  "HEIMDAL_PORT",
		},
		Session: SessionConfig{
			RunIDEnv: "HEIMDAL_RUN_ID",
			PortEnv:  "HEIMDAL_PORT",
		},
		Artifacts: ArtifactConfig{Directory: defaultArtifactDir},
	}
}

func loadConfig(root string, detectedConfig string) (Config, string, error) {
	cfg := defaultConfig(detectedConfig)
	path := filepath.Join(root, configFileName)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, "", nil
	}
	if err != nil {
		return Config{}, path, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(contents, &cfg); err != nil {
		return Config{}, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Artifacts.Directory == "" {
		cfg.Artifacts.Directory = defaultArtifactDir
	}
	if cfg.Playwright.Config == "" {
		cfg.Playwright.Config = detectedConfig
	}
	if cfg.Playwright.RunIDEnv == "" {
		cfg.Playwright.RunIDEnv = "HEIMDAL_RUN_ID"
	}
	if cfg.Session.RunIDEnv == "" {
		cfg.Session.RunIDEnv = cfg.Playwright.RunIDEnv
	}
	if cfg.Session.PortEnv == "" {
		cfg.Session.PortEnv = cfg.Playwright.PortEnv
	}
	return cfg, path, nil
}

func artifactRoot(project Project, override string) string {
	directory := override
	if directory == "" {
		directory = project.Config.Artifacts.Directory
	}
	if directory == "" {
		directory = defaultArtifactDir
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(project.Root, directory)
	}
	return filepath.Clean(directory)
}

func configRelativePath(root, configured string) string {
	if configured == "" {
		return ""
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(root, configured)
}

func writeProjectConfig(path string, cfg Config, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to replace it)", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check %s: %w", path, err)
		}
	}
	contents, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func hasPlaywrightConfig(root string) string {
	for _, name := range []string{
		"playwright.config.ts",
		"playwright.config.mts",
		"playwright.config.js",
		"playwright.config.mjs",
		"playwright.config.cjs",
	} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return name
		}
	}
	return ""
}

func sanitize(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
