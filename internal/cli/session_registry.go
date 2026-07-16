package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sessionIndex struct {
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	Root          string    `json:"root"`
	StatePath     string    `json:"state_path"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func sessionRegistryDirectory() (string, error) {
	if configured := os.Getenv("HEIMDAL_STATE_DIR"); configured != "" {
		return filepath.Join(configured, "sessions"), nil
	}
	if configured := os.Getenv("XDG_STATE_HOME"); configured != "" {
		return filepath.Join(configured, "heimdal", "sessions"), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve Heimdal session state directory: %w", err)
	}
	return filepath.Join(configDir, "heimdal", "sessions"), nil
}

func sessionIndexPath(state SessionState) (string, error) {
	directory, err := sessionRegistryDirectory()
	if err != nil {
		return "", err
	}
	name := sanitize(state.Name)
	if name == "" {
		name = defaultSessionName
	}
	hash := sha256.Sum256([]byte(filepath.Clean(state.Root)))
	return filepath.Join(directory, fmt.Sprintf("%s-%x.json", name, hash[:8])), nil
}

func writeSessionIndex(state SessionState) error {
	path, err := sessionIndexPath(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Heimdal session state directory: %w", err)
	}
	index := sessionIndex{
		SchemaVersion: 1,
		Name:          state.Name,
		Root:          state.Root,
		StatePath:     filepath.Join(filepath.Dir(state.SessionDir), "session.json"),
		UpdatedAt:     time.Now().UTC(),
	}
	contents, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Heimdal session index: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write Heimdal session index: %w", err)
	}
	return nil
}

func readSessionIndexes(name string) ([]sessionIndex, error) {
	directory, err := sessionRegistryDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Heimdal session state directory: %w", err)
	}
	name = sanitize(name)
	if name == "" {
		name = defaultSessionName
	}
	var indexes []sessionIndex
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), name+"-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			continue
		}
		var index sessionIndex
		if unmarshalErr := json.Unmarshal(contents, &index); unmarshalErr != nil || index.Name != name {
			continue
		}
		if _, statErr := os.Stat(index.Root); statErr != nil {
			continue
		}
		if _, statErr := os.Stat(index.StatePath); statErr != nil {
			continue
		}
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool {
		return indexes[i].UpdatedAt.After(indexes[j].UpdatedAt)
	})
	return indexes, nil
}

func discoverSession(options SessionOptions) (Project, SessionState, string, error) {
	if options.Root != "" {
		project, err := Discover(options.Root)
		if err != nil {
			return Project{}, SessionState{}, "", err
		}
		state, path, err := loadSession(project, options)
		return project, state, path, err
	}

	// Prefer the current worktree when the agent is already inside it.
	if project, err := Discover(""); err == nil {
		if state, path, loadErr := loadSession(project, options); loadErr == nil {
			return project, state, path, nil
		}
	}

	indexes, err := readSessionIndexes(options.Name)
	if err != nil {
		return Project{}, SessionState{}, "", err
	}
	if len(indexes) == 0 {
		name := sanitize(options.Name)
		if name == "" {
			name = defaultSessionName
		}
		return Project{}, SessionState{}, "", fmt.Errorf("session %q was not found from the current directory; start it with `heimdal session start --root DIR --name %s` or pass --root", name, name)
	}
	if len(indexes) > 1 {
		roots := make([]string, len(indexes))
		for i, index := range indexes {
			roots[i] = index.Root
		}
		return Project{}, SessionState{}, "", fmt.Errorf("session %q exists in multiple roots (%s); pass --root", indexes[0].Name, strings.Join(roots, ", "))
	}
	index := indexes[0]
	project, err := Discover(index.Root)
	if err != nil {
		return Project{}, SessionState{}, "", err
	}
	state, err := readSessionState(index.StatePath)
	if err != nil {
		return Project{}, SessionState{}, index.StatePath, fmt.Errorf("read session %q: %w", index.Name, err)
	}
	return project, state, index.StatePath, nil
}
