package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	coordinationSchemaVersion      = 1
	coordinationMaxMetadataBytes   = 64 * 1024
	coordinationDefaultWaitTimeout = time.Minute
	coordinationMaxWaitTimeout     = 24 * time.Hour
	coordinationPollInterval       = 50 * time.Millisecond
)

var coordinationVersionCounter uint64

// coordinationSelection contains the options shared by metadata and signal
// commands. DirSet and RunSet are deliberately retained separately from their
// values: an explicitly supplied option changes the HEIMDAL_RUN_DIR
// precedence rules even when its value is "latest" or an empty project-root
// default.
type coordinationSelection struct {
	Dir    string
	Run    string
	JSON   bool
	DirSet bool
	RunSet bool
}

type metadataPublishOptions struct {
	coordinationSelection
	Namespace string
	File      string
	FileSet   bool
}

type metadataGetOptions struct {
	coordinationSelection
	Namespace string
}

type signalSendOptions struct {
	coordinationSelection
	Name string
}

type signalWaitOptions struct {
	coordinationSelection
	Name       string
	Timeout    time.Duration
	TimeoutSet bool
}

type coordinationRun struct {
	ID  string
	Dir string
}

type metadataPublication struct {
	Namespace string
	Version   string
	Payload   json.RawMessage
}

type metadataPublishResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	Namespace     string `json:"namespace"`
	Version       string `json:"version"`
}

type metadataGetResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	Namespace     string `json:"namespace,omitempty"`
	Metadata      any    `json:"metadata"`
}

type signalResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	Signal        string `json:"signal"`
	AlreadySent   bool   `json:"already_sent,omitempty"`
}

type coordinationErrorResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Error         string `json:"error"`
}

// runMetadata handles run-scoped metadata without coupling its storage contract
// to Playwright execution.
func runMetadata(ctx context.Context, args []string, out, errOut io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(args) == 0 {
		return coordinationReportError(false, errors.New("metadata requires publish or get"), out, errOut, 2)
	}

	switch args[0] {
	case "publish":
		options, err := parseMetadataPublishOptions(args[1:])
		if err != nil {
			return coordinationReportError(coordinationJSONRequested(args[1:]), err, out, errOut, 2)
		}
		if err := ctx.Err(); err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		run, err := resolveCoordinationRun(options.coordinationSelection)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		payload, err := readMetadataPayload(ctx, options.File)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		version, err := publishCoordinationMetadata(run.Dir, options.Namespace, payload)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		response := metadataPublishResponse{
			SchemaVersion: coordinationSchemaVersion,
			Status:        "published",
			RunID:         run.ID,
			Namespace:     options.Namespace,
			Version:       version,
		}
		if options.JSON {
			if err := writeJSONTo(out, response); err != nil {
				return coordinationReportError(true, fmt.Errorf("write metadata response: %w", err), out, errOut, 1)
			}
			return 0
		}
		fmt.Fprintf(out, "Published metadata %q for run %q (version %s)\n", options.Namespace, run.ID, version)
		return 0

	case "get":
		options, err := parseMetadataGetOptions(args[1:])
		if err != nil {
			return coordinationReportError(coordinationJSONRequested(args[1:]), err, out, errOut, 2)
		}
		if err := ctx.Err(); err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		run, err := resolveCoordinationRun(options.coordinationSelection)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}

		if options.Namespace != "" {
			publication, err := latestCoordinationMetadata(run.Dir, options.Namespace)
			if err != nil {
				return coordinationReportError(options.JSON, err, out, errOut, 1)
			}
			if options.JSON {
				response := metadataGetResponse{
					SchemaVersion: coordinationSchemaVersion,
					Status:        "ok",
					RunID:         run.ID,
					Namespace:     options.Namespace,
					Metadata:      publication.Payload,
				}
				if err := writeJSONTo(out, response); err != nil {
					return coordinationReportError(true, fmt.Errorf("write metadata response: %w", err), out, errOut, 1)
				}
				return 0
			}
			if err := writeRawCoordinationPayload(out, publication.Payload); err != nil {
				return coordinationReportError(false, fmt.Errorf("write metadata: %w", err), out, errOut, 1)
			}
			return 0
		}

		payloads, err := allCoordinationMetadata(run.Dir)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		if options.JSON {
			response := metadataGetResponse{
				SchemaVersion: coordinationSchemaVersion,
				Status:        "ok",
				RunID:         run.ID,
				Metadata:      payloads,
			}
			if err := writeJSONTo(out, response); err != nil {
				return coordinationReportError(true, fmt.Errorf("write metadata response: %w", err), out, errOut, 1)
			}
			return 0
		}
		if err := writeJSONTo(out, payloads); err != nil {
			return coordinationReportError(false, fmt.Errorf("write metadata: %w", err), out, errOut, 1)
		}
		return 0

	default:
		return coordinationReportError(coordinationJSONRequested(args), fmt.Errorf("unknown metadata command %q", args[0]), out, errOut, 2)
	}
}

// runSignal handles idempotent run-scoped milestones.
func runSignal(ctx context.Context, args []string, out, errOut io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(args) == 0 {
		return coordinationReportError(false, errors.New("signal requires send or wait"), out, errOut, 2)
	}

	switch args[0] {
	case "send":
		options, err := parseSignalSendOptions(args[1:])
		if err != nil {
			return coordinationReportError(coordinationJSONRequested(args[1:]), err, out, errOut, 2)
		}
		if err := ctx.Err(); err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		run, err := resolveCoordinationRun(options.coordinationSelection)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		created, err := sendCoordinationSignal(run.Dir, options.Name)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		response := signalResponse{
			SchemaVersion: coordinationSchemaVersion,
			Status:        "sent",
			RunID:         run.ID,
			Signal:        options.Name,
			AlreadySent:   !created,
		}
		if !created {
			response.Status = "already_sent"
		}
		if options.JSON {
			if err := writeJSONTo(out, response); err != nil {
				return coordinationReportError(true, fmt.Errorf("write signal response: %w", err), out, errOut, 1)
			}
			return 0
		}
		if created {
			fmt.Fprintf(out, "Signal %q sent for run %q\n", options.Name, run.ID)
		} else {
			fmt.Fprintf(out, "Signal %q was already sent for run %q\n", options.Name, run.ID)
		}
		return 0

	case "wait":
		options, err := parseSignalWaitOptions(args[1:])
		if err != nil {
			return coordinationReportError(coordinationJSONRequested(args[1:]), err, out, errOut, 2)
		}
		if err := ctx.Err(); err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		run, err := resolveCoordinationRun(options.coordinationSelection)
		if err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		if err := waitForCoordinationSignal(ctx, run.Dir, options.Name, options.Timeout); err != nil {
			return coordinationReportError(options.JSON, err, out, errOut, 1)
		}
		response := signalResponse{
			SchemaVersion: coordinationSchemaVersion,
			Status:        "received",
			RunID:         run.ID,
			Signal:        options.Name,
		}
		if options.JSON {
			if err := writeJSONTo(out, response); err != nil {
				return coordinationReportError(true, fmt.Errorf("write signal response: %w", err), out, errOut, 1)
			}
			return 0
		}
		fmt.Fprintf(out, "Signal %q received for run %q\n", options.Name, run.ID)
		return 0

	default:
		return coordinationReportError(coordinationJSONRequested(args), fmt.Errorf("unknown signal command %q", args[0]), out, errOut, 2)
	}
}

func parseMetadataPublishOptions(args []string) (metadataPublishOptions, error) {
	options := metadataPublishOptions{}
	var positionals []string
	for i := 0; i < len(args); i++ {
		next, handled, err := parseCoordinationSelectionOption(args, i, &options.coordinationSelection)
		if err != nil {
			return options, err
		}
		if handled {
			i = next
			continue
		}
		if value, next, ok, err := coordinationOptionValue(args, i, "--file"); ok {
			if err != nil {
				return options, err
			}
			if options.FileSet {
				return options, errors.New("--file may only be specified once")
			}
			options.File = value
			options.FileSet = true
			i = next
			continue
		}
		if args[i] == "--help" || args[i] == "-h" {
			return options, errors.New("metadata publish requires a namespace")
		}
		if strings.HasPrefix(args[i], "-") {
			return options, fmt.Errorf("unknown metadata publish option %q", args[i])
		}
		positionals = append(positionals, args[i])
	}
	if len(positionals) != 1 {
		return options, errors.New("metadata publish requires exactly one namespace")
	}
	options.Namespace = positionals[0]
	if err := validateCoordinationSelector("namespace", options.Namespace); err != nil {
		return options, err
	}
	if options.RunSet {
		if err := validateCoordinationRunSelector(options.Run); err != nil {
			return options, err
		}
	}
	return options, nil
}

func parseMetadataGetOptions(args []string) (metadataGetOptions, error) {
	options := metadataGetOptions{}
	var positionals []string
	for i := 0; i < len(args); i++ {
		next, handled, err := parseCoordinationSelectionOption(args, i, &options.coordinationSelection)
		if err != nil {
			return options, err
		}
		if handled {
			i = next
			continue
		}
		if args[i] == "--help" || args[i] == "-h" {
			return options, errors.New("metadata get accepts an optional namespace")
		}
		if strings.HasPrefix(args[i], "-") {
			return options, fmt.Errorf("unknown metadata get option %q", args[i])
		}
		positionals = append(positionals, args[i])
	}
	if len(positionals) > 1 {
		return options, errors.New("metadata get accepts at most one namespace")
	}
	if len(positionals) == 1 {
		options.Namespace = positionals[0]
		if err := validateCoordinationSelector("namespace", options.Namespace); err != nil {
			return options, err
		}
	}
	if options.RunSet {
		if err := validateCoordinationRunSelector(options.Run); err != nil {
			return options, err
		}
	}
	return options, nil
}

func parseSignalSendOptions(args []string) (signalSendOptions, error) {
	options := signalSendOptions{}
	var positionals []string
	for i := 0; i < len(args); i++ {
		next, handled, err := parseCoordinationSelectionOption(args, i, &options.coordinationSelection)
		if err != nil {
			return options, err
		}
		if handled {
			i = next
			continue
		}
		if args[i] == "--help" || args[i] == "-h" {
			return options, errors.New("signal send requires a name")
		}
		if strings.HasPrefix(args[i], "-") {
			return options, fmt.Errorf("unknown signal send option %q", args[i])
		}
		positionals = append(positionals, args[i])
	}
	if len(positionals) != 1 {
		return options, errors.New("signal send requires exactly one name")
	}
	options.Name = positionals[0]
	if err := validateCoordinationSelector("signal", options.Name); err != nil {
		return options, err
	}
	if options.RunSet {
		if err := validateCoordinationRunSelector(options.Run); err != nil {
			return options, err
		}
	}
	return options, nil
}

func parseSignalWaitOptions(args []string) (signalWaitOptions, error) {
	options := signalWaitOptions{Timeout: coordinationDefaultWaitTimeout}
	var positionals []string
	for i := 0; i < len(args); i++ {
		next, handled, err := parseCoordinationSelectionOption(args, i, &options.coordinationSelection)
		if err != nil {
			return options, err
		}
		if handled {
			i = next
			continue
		}
		if value, next, ok, err := coordinationOptionValue(args, i, "--timeout"); ok {
			if err != nil {
				return options, err
			}
			if options.TimeoutSet {
				return options, errors.New("--timeout may only be specified once")
			}
			timeout, parseErr := time.ParseDuration(value)
			if parseErr != nil || timeout <= 0 {
				return options, fmt.Errorf("--timeout must be a positive duration (got %q)", value)
			}
			if timeout > coordinationMaxWaitTimeout {
				return options, fmt.Errorf("--timeout must not exceed %s", coordinationMaxWaitTimeout)
			}
			options.Timeout = timeout
			options.TimeoutSet = true
			i = next
			continue
		}
		if args[i] == "--help" || args[i] == "-h" {
			return options, errors.New("signal wait requires a name")
		}
		if strings.HasPrefix(args[i], "-") {
			return options, fmt.Errorf("unknown signal wait option %q", args[i])
		}
		positionals = append(positionals, args[i])
	}
	if len(positionals) != 1 {
		return options, errors.New("signal wait requires exactly one name")
	}
	options.Name = positionals[0]
	if err := validateCoordinationSelector("signal", options.Name); err != nil {
		return options, err
	}
	if options.RunSet {
		if err := validateCoordinationRunSelector(options.Run); err != nil {
			return options, err
		}
	}
	return options, nil
}

func parseCoordinationSelectionOption(args []string, index int, options *coordinationSelection) (int, bool, error) {
	arg := args[index]
	if arg == "--json" {
		if options.JSON {
			return index, true, errors.New("--json may only be specified once")
		}
		options.JSON = true
		return index, true, nil
	}
	if strings.HasPrefix(arg, "--json=") {
		return index, true, errors.New("--json does not take a value")
	}
	for _, flag := range []string{"--dir", "--root"} {
		if value, next, ok, err := coordinationOptionValue(args, index, flag); ok {
			if err != nil {
				return index, true, err
			}
			if options.DirSet && filepath.Clean(options.Dir) != filepath.Clean(value) {
				return index, true, errors.New("--dir and --root cannot specify different paths")
			}
			options.Dir = value
			options.DirSet = true
			return next, true, nil
		}
	}
	if value, next, ok, err := coordinationOptionValue(args, index, "--run"); ok {
		if err != nil {
			return index, true, err
		}
		if options.RunSet {
			return index, true, errors.New("--run may only be specified once")
		}
		options.Run = value
		options.RunSet = true
		return next, true, nil
	}
	return index, false, nil
}

func coordinationOptionValue(args []string, index int, flag string) (string, int, bool, error) {
	arg := args[index]
	if strings.HasPrefix(arg, flag+"=") {
		value := strings.TrimPrefix(arg, flag+"=")
		if value == "" {
			return "", index, true, fmt.Errorf("%s requires a value", flag)
		}
		return value, index, true, nil
	}
	if arg != flag {
		return "", index, false, nil
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return "", index, true, fmt.Errorf("%s requires a value", flag)
	}
	return args[index+1], index + 1, true, nil
}

func coordinationJSONRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func validateCoordinationSelector(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%s selector must not be empty", kind)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("invalid %s selector %q", kind, value)
	}
	if len(value) > 255 {
		return fmt.Errorf("%s selector is too long", kind)
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		letterOrDigit := (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')
		if index == 0 {
			if !letterOrDigit {
				return fmt.Errorf("invalid %s selector %q", kind, value)
			}
			continue
		}
		if !letterOrDigit && character != '-' && character != '_' && character != '.' {
			return fmt.Errorf("invalid %s selector %q", kind, value)
		}
	}
	return nil
}

func validateCoordinationRunSelector(value string) error {
	if value == "latest" {
		return nil
	}
	if !validArtifactID(value) {
		return errors.New("run id must contain only lowercase letters, numbers, and hyphens")
	}
	return nil
}

func resolveCoordinationRun(selection coordinationSelection) (coordinationRun, error) {
	if !selection.RunSet && !selection.DirSet {
		if direct, ok := os.LookupEnv("HEIMDAL_RUN_DIR"); ok && direct != "" {
			absolute, err := filepath.Abs(direct)
			if err != nil {
				return coordinationRun{}, fmt.Errorf("resolve HEIMDAL_RUN_DIR: %w", err)
			}
			absolute = filepath.Clean(absolute)
			if err := validateCoordinationRunDirectory(absolute); err != nil {
				return coordinationRun{}, fmt.Errorf("HEIMDAL_RUN_DIR: %w", err)
			}
			id := filepath.Base(absolute)
			if err := validateCoordinationRunSelector(id); err != nil {
				return coordinationRun{}, fmt.Errorf("HEIMDAL_RUN_DIR does not name a valid run: %w", err)
			}
			return coordinationRun{ID: id, Dir: absolute}, nil
		}
	}

	project, err := Discover(selection.Dir)
	if err != nil {
		return coordinationRun{}, err
	}
	root := artifactRoot(project, "")
	runID := selection.Run
	if runID == "" {
		runID = "latest"
	}
	if err := validateCoordinationRunSelector(runID); err != nil {
		return coordinationRun{}, err
	}
	if runID == "latest" {
		return latestCoordinationRun(root)
	}
	runDir := filepath.Join(root, runID)
	if err := validateCoordinationRunDirectory(runDir); err != nil {
		return coordinationRun{}, fmt.Errorf("run %q: %w", runID, err)
	}
	return coordinationRun{ID: runID, Dir: runDir}, nil
}

func validateCoordinationRunDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("run directory does not exist: %s", path)
		}
		return fmt.Errorf("inspect run directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("run directory must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("run path is not a directory: %s", path)
	}
	return nil
}

type coordinationRunCandidate struct {
	id      string
	path    string
	started time.Time
}

func latestCoordinationRun(root string) (coordinationRun, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return coordinationRun{}, fmt.Errorf("no Heimdal runs found in %s", root)
		}
		return coordinationRun{}, fmt.Errorf("read artifact directory %s: %w", root, err)
	}

	candidates := make([]coordinationRunCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if err := validateCoordinationRunSelector(entry.Name()); err != nil {
			continue
		}
		runDir := filepath.Join(root, entry.Name())
		info, err := os.Lstat(runDir)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		started, ok := coordinationRunStartedAt(runDir)
		if !ok {
			continue
		}
		candidates = append(candidates, coordinationRunCandidate{
			id:      entry.Name(),
			path:    runDir,
			started: started,
		})
	}
	if len(candidates) == 0 {
		return coordinationRun{}, fmt.Errorf("no active or completed Heimdal runs found in %s", root)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].started.Equal(candidates[j].started) {
			return candidates[i].started.After(candidates[j].started)
		}
		return candidates[i].id > candidates[j].id
	})
	return coordinationRun{ID: candidates[0].id, Dir: candidates[0].path}, nil
}

func coordinationRunStartedAt(runDir string) (time.Time, bool) {
	if result, err := readResult(filepath.Join(runDir, "result.json")); err == nil &&
		result.SchemaVersion == 1 && result.RunID == filepath.Base(runDir) && !result.StartedAt.IsZero() {
		return result.StartedAt, true
	}
	if manifest, err := readRunManifest(filepath.Join(runDir, "run.json")); err == nil &&
		manifest.RunID == filepath.Base(runDir) && !manifest.StartedAt.IsZero() {
		return manifest.StartedAt, true
	}
	return time.Time{}, false
}

func readMetadataPayload(ctx context.Context, filePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		payload []byte
		err     error
	)
	if filePath == "" || filePath == "-" {
		payload, err = readCoordinationReader(os.Stdin, coordinationMaxMetadataBytes)
	} else {
		payload, err = readCoordinationFile(filePath, coordinationMaxMetadataBytes)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !json.Valid(payload) {
		return nil, errors.New("metadata payload must be valid JSON")
	}
	return payload, nil
}

func readCoordinationReader(reader io.Reader, max int) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, int64(max)+1))
	if err != nil {
		return nil, fmt.Errorf("read metadata payload: %w", err)
	}
	if len(contents) > max {
		return nil, fmt.Errorf("metadata payload exceeds %d bytes", max)
	}
	return contents, nil
}

func readCoordinationFile(path string, max int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	contents, err := readCoordinationReader(file, max)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return contents, nil
}

func publishCoordinationMetadata(runDir, namespace string, payload []byte) (string, error) {
	if err := validateCoordinationSelector("namespace", namespace); err != nil {
		return "", err
	}
	if len(payload) > coordinationMaxMetadataBytes {
		return "", fmt.Errorf("metadata payload exceeds %d bytes", coordinationMaxMetadataBytes)
	}
	if !json.Valid(payload) {
		return "", errors.New("metadata payload must be valid JSON")
	}
	metadataRoot := filepath.Join(runDir, "metadata")
	if err := ensureOwnerDirectory(metadataRoot); err != nil {
		return "", fmt.Errorf("prepare metadata directory: %w", err)
	}
	namespaceDir := filepath.Join(metadataRoot, namespace)
	if err := ensureOwnerDirectory(namespaceDir); err != nil {
		return "", fmt.Errorf("prepare metadata namespace: %w", err)
	}

	for attempt := 0; attempt < 16; attempt++ {
		version := newCoordinationVersion()
		path := filepath.Join(namespaceDir, version+".json")
		created, err := writeImmutableCoordinationFile(namespaceDir, path, payload)
		if err != nil {
			return "", fmt.Errorf("publish metadata: %w", err)
		}
		if created {
			return version, nil
		}
	}
	return "", errors.New("publish metadata: could not allocate a unique version")
}

func newCoordinationVersion() string {
	sequence := atomic.AddUint64(&coordinationVersionCounter, 1)
	return fmt.Sprintf("%020d-%06d-%d", time.Now().UTC().UnixNano(), sequence, os.Getpid())
}

func latestCoordinationMetadata(runDir, namespace string) (metadataPublication, error) {
	if err := validateCoordinationSelector("namespace", namespace); err != nil {
		return metadataPublication{}, err
	}
	metadataRoot := filepath.Join(runDir, "metadata")
	exists, err := openOwnerDirectory(metadataRoot)
	if err != nil {
		return metadataPublication{}, fmt.Errorf("inspect metadata directory: %w", err)
	}
	if !exists {
		return metadataPublication{}, fmt.Errorf("metadata namespace %q has no publications", namespace)
	}
	namespaceDir := filepath.Join(metadataRoot, namespace)
	exists, err = openOwnerDirectory(namespaceDir)
	if err != nil {
		return metadataPublication{}, fmt.Errorf("inspect metadata namespace: %w", err)
	}
	if !exists {
		return metadataPublication{}, fmt.Errorf("metadata namespace %q has no publications", namespace)
	}
	publication, found, err := latestCoordinationMetadataInDirectory(namespaceDir, namespace)
	if err != nil {
		return metadataPublication{}, err
	}
	if !found {
		return metadataPublication{}, fmt.Errorf("metadata namespace %q has no publications", namespace)
	}
	return publication, nil
}

func allCoordinationMetadata(runDir string) (map[string]json.RawMessage, error) {
	payloads := make(map[string]json.RawMessage)
	metadataRoot := filepath.Join(runDir, "metadata")
	exists, err := openOwnerDirectory(metadataRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect metadata directory: %w", err)
	}
	if !exists {
		return payloads, nil
	}
	entries, err := os.ReadDir(metadataRoot)
	if err != nil {
		return nil, fmt.Errorf("read metadata directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		namespace := entry.Name()
		if validateCoordinationSelector("namespace", namespace) != nil {
			continue
		}
		namespaceDir := filepath.Join(metadataRoot, namespace)
		exists, err := openOwnerDirectory(namespaceDir)
		if err != nil {
			return nil, fmt.Errorf("inspect metadata namespace %q: %w", namespace, err)
		}
		if !exists {
			continue
		}
		publication, found, err := latestCoordinationMetadataInDirectory(namespaceDir, namespace)
		if err != nil {
			return nil, err
		}
		if found {
			payloads[namespace] = publication.Payload
		}
	}
	return payloads, nil
}

type metadataFileCandidate struct {
	name string
	path string
}

func latestCoordinationMetadataInDirectory(namespaceDir, namespace string) (metadataPublication, bool, error) {
	entries, err := os.ReadDir(namespaceDir)
	if err != nil {
		return metadataPublication{}, false, fmt.Errorf("read metadata namespace %q: %w", namespace, err)
	}
	candidates := make([]metadataFileCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		version := strings.TrimSuffix(entry.Name(), ".json")
		if !validCoordinationVersion(version) {
			return metadataPublication{}, false, fmt.Errorf("invalid metadata publication name %q", entry.Name())
		}
		path := filepath.Join(namespaceDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return metadataPublication{}, false, fmt.Errorf("inspect metadata publication: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return metadataPublication{}, false, fmt.Errorf("metadata publication is not a regular file: %s", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return metadataPublication{}, false, fmt.Errorf("protect metadata publication: %w", err)
		}
		candidates = append(candidates, metadataFileCandidate{
			name: entry.Name(),
			path: path,
		})
	}
	if len(candidates) == 0 {
		return metadataPublication{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name > candidates[j].name })
	payload, err := readCoordinationFile(candidates[0].path, coordinationMaxMetadataBytes)
	if err != nil {
		return metadataPublication{}, false, err
	}
	if !json.Valid(payload) {
		return metadataPublication{}, false, fmt.Errorf("metadata publication %s is not valid JSON", candidates[0].name)
	}
	return metadataPublication{
		Namespace: namespace,
		Version:   strings.TrimSuffix(candidates[0].name, ".json"),
		Payload:   json.RawMessage(payload),
	}, true, nil
}

func validCoordinationVersion(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 3 || len(parts[0]) != 20 || len(parts[1]) < 6 || parts[2] == "" {
		return false
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func ensureOwnerDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path is not an owner-only directory: %s", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect %s: %w", path, err)
	}
	return nil
}

func openOwnerDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("path is not an owner-only directory: %s", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return false, fmt.Errorf("protect %s: %w", path, err)
	}
	return true, nil
}

func writeImmutableCoordinationFile(directory, finalPath string, contents []byte) (bool, error) {
	temporary, err := os.CreateTemp(directory, ".heimdal-coordination-")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return false, err
	}
	written, err := temporary.Write(contents)
	if err != nil {
		_ = temporary.Close()
		return false, err
	}
	if written != len(contents) {
		_ = temporary.Close()
		return false, io.ErrShortWrite
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}

	if err := os.Link(temporaryPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			if fileErr := ensureOwnerRegularFile(finalPath); fileErr != nil {
				return false, fileErr
			}
			return false, nil
		}
		return false, err
	}
	cleanup = false
	if err := os.Remove(temporaryPath); err != nil {
		return true, err
	}
	if err := os.Chmod(finalPath, 0o600); err != nil {
		return true, err
	}
	return true, nil
}

func ensureOwnerRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("path is not an owner-only regular file: %s", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect %s: %w", path, err)
	}
	return nil
}

func sendCoordinationSignal(runDir, name string) (bool, error) {
	if err := validateCoordinationSelector("signal", name); err != nil {
		return false, err
	}
	signalsDir := filepath.Join(runDir, "signals")
	if err := ensureOwnerDirectory(signalsDir); err != nil {
		return false, fmt.Errorf("prepare signal directory: %w", err)
	}
	path := filepath.Join(signalsDir, name)
	if _, err := os.Lstat(path); err == nil {
		if err := ensureOwnerRegularFile(path); err != nil {
			return false, fmt.Errorf("inspect signal %q: %w", name, err)
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect signal %q: %w", name, err)
	}

	record, err := json.Marshal(struct {
		SchemaVersion int       `json:"schema_version"`
		Signal        string    `json:"signal"`
		SentAt        time.Time `json:"sent_at"`
	}{
		SchemaVersion: coordinationSchemaVersion,
		Signal:        name,
		SentAt:        time.Now().UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("encode signal %q: %w", name, err)
	}
	record = append(record, '\n')
	created, err := writeImmutableCoordinationFile(signalsDir, path, record)
	if err != nil {
		return false, fmt.Errorf("send signal %q: %w", name, err)
	}
	if !created {
		if err := ensureOwnerRegularFile(path); err != nil {
			return false, fmt.Errorf("inspect signal %q: %w", name, err)
		}
	}
	return created, nil
}

func waitForCoordinationSignal(ctx context.Context, runDir, name string, timeout time.Duration) error {
	if err := validateCoordinationSelector("signal", name); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 || timeout > coordinationMaxWaitTimeout {
		return fmt.Errorf("signal wait timeout must be between 0 and %s", coordinationMaxWaitTimeout)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	check := func() (bool, error) {
		signalsDir := filepath.Join(runDir, "signals")
		exists, err := openOwnerDirectory(signalsDir)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
		path := filepath.Join(signalsDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect signal %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("signal %q is not a regular file", name)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return false, fmt.Errorf("protect signal %q: %w", name, err)
		}
		return true, nil
	}

	ticker := time.NewTicker(coordinationPollInterval)
	defer ticker.Stop()
	for {
		present, err := check()
		if err != nil {
			return err
		}
		if present {
			return nil
		}
		select {
		case <-waitContext.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("timed out waiting for signal %q", name)
		case <-ticker.C:
		}
	}
}

func writeRawCoordinationPayload(out io.Writer, payload []byte) error {
	if _, err := out.Write(payload); err != nil {
		return err
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		_, err := io.WriteString(out, "\n")
		return err
	}
	return nil
}

func coordinationReportError(asJSON bool, err error, out, errOut io.Writer, exitCode int) int {
	if asJSON {
		_ = writeJSONTo(out, coordinationErrorResponse{
			SchemaVersion: coordinationSchemaVersion,
			Status:        "error",
			Error:         err.Error(),
		})
	} else {
		fmt.Fprintln(errOut, "heimdal:", err)
	}
	return exitCode
}
