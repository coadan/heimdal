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
	return sessionIndexPathForRoot(directory, state.Name, state.Root), nil
}

func sessionIndexPathForRoot(directory, name, root string) string {
	name = sanitize(name)
	if name == "" {
		name = defaultSessionName
	}
	hash := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(directory, fmt.Sprintf("%s-%x.json", name, hash[:8]))
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
	if options.Actor != "" || options.Group != "" {
		resolved, err := resolveSessionGroupActor(options)
		if err != nil {
			return Project{}, SessionState{}, "", err
		}
		return discoverSession(resolved)
	}
	start := options.Root
	if start == "" {
		start = "."
	}
	if absolute, absoluteErr := filepath.Abs(start); absoluteErr == nil {
		if info, statErr := os.Stat(absolute); statErr == nil && !info.IsDir() {
			absolute = filepath.Dir(absolute)
		}
		if index, ok, indexErr := sessionIndexFromPath(options.Name, absolute); indexErr != nil {
			return Project{}, SessionState{}, "", indexErr
		} else if ok {
			return loadIndexedSession(index)
		}
	}

	if options.Root != "" {
		project, discoverErr := Discover(options.Root)
		if discoverErr != nil {
			return Project{}, SessionState{}, "", discoverErr
		}
		state, path, loadErr := loadSession(project, options)
		if loadErr == nil {
			refreshSessionProjectCache(&state, project)
			_ = writeSessionState(path, state)
		}
		return project, state, path, loadErr
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
		return Project{}, SessionState{}, "", fmt.Errorf("session %q was not found from the current directory; start it with `heimdal session start --dir PATH --name %s` or pass --dir", name, name)
	}
	if len(indexes) > 1 {
		roots := make([]string, len(indexes))
		for i, index := range indexes {
			roots[i] = index.Root
		}
		return Project{}, SessionState{}, "", fmt.Errorf("session %q exists in multiple worktrees (%s); pass --dir", indexes[0].Name, strings.Join(roots, ", "))
	}
	return loadIndexedSession(indexes[0])
}

func sessionIndexFromPath(name, start string) (sessionIndex, bool, error) {
	directory, err := sessionRegistryDirectory()
	if err != nil {
		return sessionIndex{}, false, err
	}
	for current := filepath.Clean(start); ; current = filepath.Dir(current) {
		path := sessionIndexPathForRoot(directory, name, current)
		contents, readErr := os.ReadFile(path)
		if readErr == nil {
			var index sessionIndex
			if json.Unmarshal(contents, &index) == nil && index.Name == normalizedSessionName(name) && filepath.Clean(index.Root) == current {
				if _, stateErr := os.Stat(index.StatePath); stateErr == nil {
					return index, true, nil
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return sessionIndex{}, false, fmt.Errorf("read Heimdal session index: %w", readErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return sessionIndex{}, false, nil
}

func normalizedSessionName(name string) string {
	name = sanitize(name)
	if name == "" {
		return defaultSessionName
	}
	return name
}

func loadIndexedSession(index sessionIndex) (Project, SessionState, string, error) {
	state, err := readSessionState(index.StatePath)
	if err != nil {
		return Project{}, SessionState{}, index.StatePath, fmt.Errorf("read session %q: %w", index.Name, err)
	}
	if project, ok, cacheErr := cachedSessionProject(state); cacheErr != nil {
		return Project{}, SessionState{}, index.StatePath, cacheErr
	} else if ok {
		return project, state, index.StatePath, nil
	}
	project, err := Discover(index.Root)
	if err != nil {
		return Project{}, SessionState{}, index.StatePath, err
	}
	refreshSessionProjectCache(&state, project)
	if err := writeSessionState(index.StatePath, state); err != nil {
		return Project{}, SessionState{}, index.StatePath, err
	}
	return project, state, index.StatePath, nil
}

func refreshSessionProjectCache(state *SessionState, project Project) {
	configFile := project.ConfigFile
	if configFile == "" {
		configFile = filepath.Join(project.Root, configFileName)
	}
	state.ProjectCache = &SessionProjectCache{
		ConfigFile:  configFile,
		ConfigStamp: sessionFileStamp(configFile),
		AgentRunner: append([]string(nil), project.AgentRunner...),
	}
}

func cachedSessionProject(state SessionState) (Project, bool, error) {
	cache := state.ProjectCache
	if cache == nil || len(cache.AgentRunner) == 0 {
		return Project{}, false, nil
	}
	stamp := sessionFileStamp(cache.ConfigFile)
	if stamp == "error" || cache.ConfigStamp != stamp {
		return Project{}, false, nil
	}
	config, configFile, err := loadConfig(state.Root, "")
	if err != nil {
		return Project{}, false, err
	}
	return Project{
		Root:        state.Root,
		Branch:      state.Branch,
		Config:      config,
		ConfigFile:  configFile,
		AgentRunner: append([]string(nil), cache.AgentRunner...),
	}, true, nil
}

func sessionFileStamp(path string) string {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		return "error"
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}
