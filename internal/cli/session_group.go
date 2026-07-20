package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	minSessionGroupActors = 2
	maxSessionGroupActors = 8
)

type SessionGroupActor struct {
	Name      string `json:"name"`
	Session   string `json:"session"`
	StatePath string `json:"state_path"`
	Owner     bool   `json:"owner,omitempty"`
	Started   bool   `json:"started"`
}

type SessionGroupState struct {
	SchemaVersion int                 `json:"schema_version"`
	Name          string              `json:"name"`
	RunID         string              `json:"run_id"`
	Root          string              `json:"root"`
	URL           string              `json:"url,omitempty"`
	Port          int                 `json:"port,omitempty"`
	Status        string              `json:"status"`
	Actors        []SessionGroupActor `json:"actors"`
	StartedAt     time.Time           `json:"started_at"`
	StoppedAt     *time.Time          `json:"stopped_at,omitempty"`
	Error         string              `json:"error,omitempty"`
}

type SessionGroupActorStatus struct {
	Name    string `json:"name"`
	Session string `json:"session"`
	Owner   bool   `json:"owner,omitempty"`
	Status  string `json:"status"`
	Server  string `json:"server,omitempty"`
}

type SessionGroupResponse struct {
	SchemaVersion int                       `json:"schema_version"`
	Status        string                    `json:"status"`
	Group         string                    `json:"group"`
	RunID         string                    `json:"run_id,omitempty"`
	Root          string                    `json:"root,omitempty"`
	URL           string                    `json:"url,omitempty"`
	Port          int                       `json:"port,omitempty"`
	Actors        []SessionGroupActorStatus `json:"actors"`
	Error         string                    `json:"error,omitempty"`
	Artifacts     map[string]string         `json:"artifacts,omitempty"`
}

type sessionGroupOptions struct {
	SessionOptions
	Actors []string
}

const sessionGroupUsage = `Heimdal bounded multi-actor session groups

Usage:
  heimdal session group start --actors host,guest [--name GROUP] [start options]
  heimdal session group status [--name GROUP] [--dir PATH] [--json]
  heimdal session group stop [--name GROUP] [--dir PATH] [--json]
  heimdal session group timeline [--name GROUP] [--dir PATH] [--json]
  heimdal session group report [--name GROUP] [--dir PATH] [--json]
`

var sessionGroupCommandUsage = map[string]string{
	"start": `Start isolated Playwright actors against one shared app fixture

Usage:
  heimdal session group start --actors ACTOR,ACTOR [--name GROUP] [--dir PATH] [--headed] [--json]

The first actor owns the configured app process. Two to eight sanitized,
unique actors are supported; a partial start is rolled back automatically.
`,
	"status": `Inspect every actor and the shared app owner in a session group

Usage:
  heimdal session group status [--name GROUP] [--dir PATH] [--json]
`,
	"stop": `Stop every actor in a group, closing the shared app owner last

Usage:
  heimdal session group stop [--name GROUP] [--dir PATH] [--json]
`,
	"timeline": `Merge actor action evidence into one time-ordered timeline

Usage:
  heimdal session group timeline [--name GROUP] [--dir PATH] [--json]
`,
	"report": `Summarize combined actor actions, failures, and evidence

Usage:
  heimdal session group report [--name GROUP] [--dir PATH] [--json]
`,
}

func sessionGroupHelpForCommand(command string) string {
	if usage, ok := sessionGroupCommandUsage[strings.ToLower(command)]; ok {
		return usage
	}
	return sessionGroupUsage
}

func runSessionGroup(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, sessionGroupUsage)
		return 0
	}
	switch args[0] {
	case "start":
		return runSessionGroupStart(ctx, args[1:], out, errOut)
	case "status":
		return runSessionGroupStatus(args[1:], out, errOut)
	case "stop":
		return runSessionGroupStop(ctx, args[1:], out, errOut)
	case "timeline":
		return runSessionGroupTimeline(args[1:], out, errOut)
	case "report":
		return runSessionGroupReport(args[1:], out, errOut)
	default:
		return reportError(false, fmt.Errorf("unknown session group command %q\n%s", args[0], sessionGroupUsage), out, errOut)
	}
}

func runSessionGroupStart(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionGroupOptions(args, true)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	group, path, err := startSessionGroup(ctx, project, options)
	if err != nil {
		response := sessionGroupResponse(group, path)
		response.Status = "failed"
		response.Error = err.Error()
		return printSessionGroupResponse(out, errOut, response, options.JSON)
	}
	response := sessionGroupResponse(group, path)
	response.Status = "started"
	return printSessionGroupResponse(out, errOut, response, options.JSON)
}

func startSessionGroup(ctx context.Context, project Project, options sessionGroupOptions) (SessionGroupState, string, error) {
	if len(project.AgentRunner) == 0 {
		return SessionGroupState{}, "", errors.New("playwright-cli is not configured; run `heimdal install agent-cli`")
	}
	name := normalizedSessionName(options.Name)
	path := sessionGroupIndexPath(project, options.Artifacts, name)
	if existing, err := readSessionGroup(path); err == nil && existing.Status != "stopped" {
		if existing.Status == "active" && !options.Force {
			return existing, path, fmt.Errorf("session group %q is already active (use --force or `heimdal session group status --name %s`)", name, name)
		}
		if stopErr := stopSessionGroupActors(ctx, project, existing); stopErr != nil {
			return existing, path, fmt.Errorf("recover existing session group %q: %w", name, stopErr)
		}
		stopped := time.Now().UTC()
		existing.Status = "stopped"
		existing.StoppedAt = &stopped
		if err := writeSessionGroup(path, existing); err != nil {
			return existing, path, err
		}
	}

	started := time.Now().UTC()
	runID := sanitize(options.RunID)
	if runID == "" {
		runID = fmt.Sprintf("%s-%s-%d", name, started.Format("20060102t150405.000000000z"), os.Getpid())
	}
	group := SessionGroupState{
		SchemaVersion: 1,
		Name:          name,
		RunID:         runID,
		Root:          project.Root,
		Status:        "starting",
		StartedAt:     started,
	}
	for index, actor := range options.Actors {
		sessionName := sessionGroupActorName(name, actor)
		group.Actors = append(group.Actors, SessionGroupActor{
			Name:      actor,
			Session:   sessionName,
			StatePath: filepath.Join(artifactRoot(project, options.Artifacts), "sessions", sessionName, "session.json"),
			Owner:     index == 0,
		})
	}
	if err := writeSessionGroup(path, group); err != nil {
		return group, path, err
	}

	ownerPort := 0
	for index := range group.Actors {
		actor := &group.Actors[index]
		actorOptions := options.SessionOptions
		actorOptions.Name = actor.Session
		actorOptions.Group = group.Name
		actorOptions.Actor = actor.Name
		actorOptions.RunID = group.RunID + "-" + actor.Name
		actorOptions.Forwarded = nil
		actorOptions.JSON = true
		if index > 0 {
			actorOptions.URL = group.URL
			actorOptions.Port = ownerPort
			actorOptions.NoServer = true
		}
		var actorOut, actorErr bytes.Buffer
		if code := startSession(ctx, project, actorOptions, &actorOut, &actorErr); code != 0 {
			startErr := sessionGroupStartError(actor.Name, actorOut.String(), actorErr.String())
			return failSessionGroupStart(ctx, project, group, path, startErr)
		}
		actor.Started = true
		if err := writeSessionGroup(path, group); err != nil {
			return failSessionGroupStart(ctx, project, group, path, err)
		}
		state, err := readSessionState(actor.StatePath)
		if err != nil {
			return failSessionGroupStart(ctx, project, group, path, fmt.Errorf("read actor %q session state: %w", actor.Name, err))
		}
		if index == 0 {
			group.URL = state.URL
			group.Port = state.Port
			ownerPort = state.Port
		}
	}
	group.Status = "active"
	if err := writeSessionGroup(path, group); err != nil {
		return failSessionGroupStart(ctx, project, group, path, err)
	}
	return group, path, nil
}

func failSessionGroupStart(ctx context.Context, project Project, group SessionGroupState, path string, startErr error) (SessionGroupState, string, error) {
	cleanupErr := stopSessionGroupActors(ctx, project, group)
	stopped := time.Now().UTC()
	group.Status = "failed"
	group.StoppedAt = &stopped
	group.Error = startErr.Error()
	writeErr := writeSessionGroup(path, group)
	return group, path, errors.Join(startErr, cleanupErr, writeErr)
}

func sessionGroupStartError(actor, stdout, stderr string) error {
	var response SessionResponse
	if json.Unmarshal([]byte(stdout), &response) == nil && response.Error != "" {
		return fmt.Errorf("start actor %q: %s", actor, response.Error)
	}
	detail := compactCLIOutput(joinOutputs(stdout, stderr))
	if detail == "" {
		detail = "session start failed"
	}
	return fmt.Errorf("start actor %q: %s", actor, detail)
}

func runSessionGroupStatus(args []string, out, errOut io.Writer) int {
	options, err := parseSessionGroupOptions(args, false)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, group, path, err := discoverSessionGroup(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	response := sessionGroupResponse(group, path)
	return printSessionGroupResponse(out, errOut, response, options.JSON)
}

func runSessionGroupStop(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionGroupOptions(args, false)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, group, path, err := discoverSessionGroup(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if group.Status != "stopped" {
		stopErr := stopSessionGroupActors(ctx, project, group)
		stopped := time.Now().UTC()
		group.StoppedAt = &stopped
		group.Status = "stopped"
		group.Error = ""
		if stopErr != nil {
			group.Status = "issues"
			group.Error = stopErr.Error()
		}
		if writeErr := writeSessionGroup(path, group); writeErr != nil {
			stopErr = errors.Join(stopErr, writeErr)
			group.Status = "issues"
			group.Error = stopErr.Error()
		}
	}
	response := sessionGroupResponse(group, path)
	if group.Status == "stopped" {
		response.Status = "stopped"
	}
	return printSessionGroupResponse(out, errOut, response, options.JSON)
}

func stopSessionGroupActors(ctx context.Context, project Project, group SessionGroupState) error {
	var stopErr error
	for index := len(group.Actors) - 1; index >= 0; index-- {
		actor := group.Actors[index]
		if actor.Owner || !actor.Started {
			continue
		}
		stopErr = errors.Join(stopErr, stopSessionGroupActor(ctx, project, actor))
	}
	for _, actor := range group.Actors {
		if actor.Owner && actor.Started {
			stopErr = errors.Join(stopErr, stopSessionGroupActor(ctx, project, actor))
			break
		}
	}
	return stopErr
}

func stopSessionGroupActor(ctx context.Context, project Project, actor SessionGroupActor) error {
	state, err := readSessionState(actor.StatePath)
	if err != nil {
		return fmt.Errorf("read actor %q session: %w", actor.Name, err)
	}
	if state.StoppedAt != nil {
		return nil
	}
	_, closeErr := runSessionCommand(ctx, project, &state, actor.StatePath, []string{"close"}, "")
	stopSessionServer(state.ServerPID)
	stopped := time.Now().UTC()
	state.StoppedAt = &stopped
	stateErr := writeSessionState(actor.StatePath, state)
	indexErr := writeSessionIndex(state)
	if err := errors.Join(closeErr, stateErr, indexErr); err != nil {
		return fmt.Errorf("stop actor %q: %w", actor.Name, err)
	}
	return nil
}

func runSessionGroupTimeline(args []string, out, errOut io.Writer) int {
	options, err := parseSessionGroupOptions(args, false)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, group, _, err := discoverSessionGroup(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionGroupTimeline(group)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.JSON {
		_ = writeJSONTo(out, timeline)
	} else {
		printSessionTimeline(out, timeline)
	}
	return 0
}

func runSessionGroupReport(args []string, out, errOut io.Writer) int {
	options, err := parseSessionGroupOptions(args, false)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, group, path, err := discoverSessionGroup(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionGroupTimeline(group)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	report := summarizeSessionGroupTimeline(group, path, timeline)
	if options.JSON {
		_ = writeJSONTo(out, report)
	} else {
		fmt.Fprintf(out, "Heimdal session group %s: %s; %d actions, %d failures, %d snapshots, %d checkpoints\n", report.Group, report.Status, report.Actions, report.Failures, report.Snapshots, report.Checkpoints)
		for _, issue := range report.Issues {
			fmt.Fprintf(out, "  issue: %s\n", issue)
		}
	}
	return boolToCode(report.Status == "issues")
}

func buildSessionGroupTimeline(group SessionGroupState) (SessionTimeline, error) {
	timeline := SessionTimeline{
		SchemaVersion: 1,
		Session:       group.Name,
		Group:         group.Name,
		RunID:         group.RunID,
		Root:          group.Root,
		URL:           group.URL,
		StartedAt:     group.StartedAt,
		StoppedAt:     group.StoppedAt,
		Entries:       []SessionTimelineEntry{},
	}
	actorOrder := make(map[string]int, len(group.Actors))
	for index, actor := range group.Actors {
		timeline.Actors = append(timeline.Actors, actor.Name)
		actorOrder[actor.Name] = index
		if !actor.Started {
			continue
		}
		state, err := readSessionState(actor.StatePath)
		if err != nil {
			return SessionTimeline{}, fmt.Errorf("read actor %q timeline: %w", actor.Name, err)
		}
		actorTimeline, err := buildSessionTimeline(state)
		if err != nil {
			return SessionTimeline{}, fmt.Errorf("build actor %q timeline: %w", actor.Name, err)
		}
		timeline.Actions += actorTimeline.Actions
		timeline.Failures += actorTimeline.Failures
		timeline.Snapshots += actorTimeline.Snapshots
		timeline.Checkpoints += actorTimeline.Checkpoints
		for _, entry := range actorTimeline.Entries {
			entry.Actor = actor.Name
			entry.ActorSequence = entry.Sequence
			timeline.Entries = append(timeline.Entries, entry)
		}
	}
	sort.SliceStable(timeline.Entries, func(i, j int) bool {
		left, right := timeline.Entries[i], timeline.Entries[j]
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.Before(right.StartedAt)
		}
		if actorOrder[left.Actor] != actorOrder[right.Actor] {
			return actorOrder[left.Actor] < actorOrder[right.Actor]
		}
		return left.ActorSequence < right.ActorSequence
	})
	for index := range timeline.Entries {
		timeline.Entries[index].Sequence = index + 1
	}
	return timeline, nil
}

func summarizeSessionGroupTimeline(group SessionGroupState, path string, timeline SessionTimeline) SessionReport {
	state := SessionState{
		Name:       group.Name,
		Group:      group.Name,
		RunID:      group.RunID,
		Root:       group.Root,
		URL:        group.URL,
		SessionDir: filepath.Dir(path),
		StartedAt:  group.StartedAt,
		StoppedAt:  group.StoppedAt,
	}
	report := summarizeSessionTimeline(state, timeline)
	report.Group = group.Name
	report.Actors = append([]string(nil), timeline.Actors...)
	report.Artifacts = map[string]string{"group": path, "directory": filepath.Dir(path)}
	if group.Status == "failed" || group.Status == "issues" {
		report.Status = "issues"
		if group.Error != "" && len(report.Issues) < 20 {
			report.Issues = append(report.Issues, group.Error)
		}
	}
	return report
}

func parseSessionGroupOptions(args []string, requireActors bool) (sessionGroupOptions, error) {
	options := sessionGroupOptions{}
	var common []string
	var actorsValue string
	for index := 0; index < len(args); index++ {
		if args[index] != "--actors" {
			common = append(common, args[index])
			continue
		}
		value, next, err := nextValue(args, index, "--actors")
		if err != nil {
			return options, err
		}
		if actorsValue != "" {
			return options, errors.New("--actors may only be specified once")
		}
		actorsValue = value
		index = next
	}
	parsed, err := parseSessionOptions(common)
	options.SessionOptions = parsed
	if err != nil {
		return options, err
	}
	if parsed.Group != "" {
		if parsed.Name != "" && normalizedSessionName(parsed.Name) != normalizedSessionName(parsed.Group) {
			return options, errors.New("--name and --group cannot select different groups")
		}
		options.Name = parsed.Group
		options.Group = ""
	}
	if options.Actor != "" {
		return options, errors.New("session group commands do not accept --actor")
	}
	if len(options.Forwarded) > 0 {
		return options, fmt.Errorf("session group command does not accept Playwright arguments: %s", strings.Join(options.Forwarded, " "))
	}
	if requireActors {
		actors, err := parseSessionGroupActors(actorsValue)
		if err != nil {
			return options, err
		}
		options.Actors = actors
	} else if actorsValue != "" {
		return options, errors.New("--actors is only valid with session group start")
	}
	return options, nil
}

func parseSessionGroupActors(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("session group start requires --actors with 2 to 8 comma-separated actor names")
	}
	parts := strings.Split(value, ",")
	if len(parts) < minSessionGroupActors || len(parts) > maxSessionGroupActors {
		return nil, fmt.Errorf("session group requires %d to %d actors (got %d)", minSessionGroupActors, maxSessionGroupActors, len(parts))
	}
	actors := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		actor := sanitize(strings.TrimSpace(part))
		if actor == "" {
			return nil, fmt.Errorf("actor %q must contain a letter or number", part)
		}
		if seen[actor] {
			return nil, fmt.Errorf("actor %q is duplicated after sanitization", actor)
		}
		seen[actor] = true
		actors = append(actors, actor)
	}
	return actors, nil
}

func resolveSessionGroupActor(options SessionOptions) (SessionOptions, error) {
	if options.Actor == "" {
		return options, errors.New("--group requires --actor for ordinary session commands")
	}
	if options.Name != "" {
		return options, errors.New("select a session with --actor/--group or --session/--name, not both")
	}
	actor := sanitize(options.Actor)
	if actor == "" {
		return options, errors.New("--actor must contain a letter or number")
	}
	project, err := Discover(options.Root)
	if err != nil {
		return options, err
	}
	groups, err := readSessionGroups(project, options.Artifacts)
	if err != nil {
		return options, err
	}
	wantedGroup := sanitize(options.Group)
	if options.Group != "" && wantedGroup == "" {
		return options, errors.New("--group must contain a letter or number")
	}
	var matches []SessionGroupState
	for _, group := range groups {
		if wantedGroup != "" && group.Name != wantedGroup {
			continue
		}
		if group.Status != "active" {
			continue
		}
		if _, ok := sessionGroupActor(group, actor); ok {
			matches = append(matches, group)
		}
	}
	if len(matches) == 0 {
		if wantedGroup != "" {
			return options, fmt.Errorf("active session group %q with actor %q was not found", wantedGroup, actor)
		}
		return options, fmt.Errorf("active session group actor %q was not found; start one with `heimdal session group start --actors ...`", actor)
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for index, group := range matches {
			names[index] = group.Name
		}
		sort.Strings(names)
		return options, fmt.Errorf("actor %q belongs to multiple active session groups (%s); pass --group GROUP", actor, strings.Join(names, ", "))
	}
	selected, _ := sessionGroupActor(matches[0], actor)
	options.Root = matches[0].Root
	options.Name = selected.Session
	options.Group = ""
	options.Actor = ""
	return options, nil
}

func discoverSessionGroup(options SessionOptions) (Project, SessionGroupState, string, error) {
	project, err := Discover(options.Root)
	if err != nil {
		return Project{}, SessionGroupState{}, "", err
	}
	groups, err := readSessionGroups(project, options.Artifacts)
	if err != nil {
		return Project{}, SessionGroupState{}, "", err
	}
	name := sanitize(options.Name)
	if name != "" {
		for _, group := range groups {
			if group.Name == name {
				return project, group, sessionGroupIndexPath(project, options.Artifacts, name), nil
			}
		}
		return Project{}, SessionGroupState{}, "", fmt.Errorf("session group %q was not found", name)
	}
	var active []SessionGroupState
	for _, group := range groups {
		if group.Status == "active" || group.Status == "starting" || group.Status == "issues" {
			active = append(active, group)
		}
	}
	candidates := active
	if len(candidates) == 0 {
		candidates = groups
	}
	if len(candidates) == 0 {
		return Project{}, SessionGroupState{}, "", errors.New("no session group was found; start one with `heimdal session group start --actors ...`")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for index, group := range candidates {
			names[index] = group.Name
		}
		sort.Strings(names)
		return Project{}, SessionGroupState{}, "", fmt.Errorf("multiple session groups match (%s); pass --name GROUP", strings.Join(names, ", "))
	}
	group := candidates[0]
	return project, group, sessionGroupIndexPath(project, options.Artifacts, group.Name), nil
}

func sessionGroupActor(group SessionGroupState, name string) (SessionGroupActor, bool) {
	for _, actor := range group.Actors {
		if actor.Name == name && actor.Started {
			return actor, true
		}
	}
	return SessionGroupActor{}, false
}

func sessionGroupActorName(group, actor string) string {
	digest := sha256.Sum256([]byte(group))
	return fmt.Sprintf("group-%x-%s", digest[:4], actor)
}

func sessionGroupIndexPath(project Project, artifacts, name string) string {
	return filepath.Join(artifactRoot(project, artifacts), "sessions", "groups", normalizedSessionName(name)+".json")
}

func readSessionGroups(project Project, artifacts string) ([]SessionGroupState, error) {
	directory := filepath.Join(artifactRoot(project, artifacts), "sessions", "groups")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session groups: %w", err)
	}
	groups := make([]SessionGroupState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		group, err := readSessionGroup(filepath.Join(directory, entry.Name()))
		if err != nil {
			continue
		}
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

func readSessionGroup(path string) (SessionGroupState, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return SessionGroupState{}, err
	}
	var group SessionGroupState
	if err := json.Unmarshal(contents, &group); err != nil {
		return SessionGroupState{}, fmt.Errorf("parse session group %s: %w", path, err)
	}
	return group, nil
}

func writeSessionGroup(path string, group SessionGroupState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session group directory: %w", err)
	}
	contents, err := json.MarshalIndent(group, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session group: %w", err)
	}
	contents = append(contents, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o644); err != nil {
		return fmt.Errorf("write session group: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace session group: %w", err)
	}
	return nil
}

func sessionGroupResponse(group SessionGroupState, path string) SessionGroupResponse {
	response := SessionGroupResponse{
		SchemaVersion: 1,
		Status:        group.Status,
		Group:         group.Name,
		RunID:         group.RunID,
		Root:          group.Root,
		URL:           group.URL,
		Port:          group.Port,
		Error:         group.Error,
		Artifacts:     map[string]string{"group": path},
	}
	for _, actor := range group.Actors {
		status := "pending"
		server := ""
		if actor.Started {
			status = "missing"
			if state, err := readSessionState(actor.StatePath); err == nil {
				if state.StoppedAt == nil {
					status = "active"
				} else {
					status = "stopped"
				}
				server = sessionServerStatus(state)
				if state.StoppedAt == nil && server == "stopped" {
					status = "issues"
				}
			}
		}
		response.Actors = append(response.Actors, SessionGroupActorStatus{Name: actor.Name, Session: actor.Session, Owner: actor.Owner, Status: status, Server: server})
	}
	if group.Status == "active" {
		for _, actor := range response.Actors {
			if actor.Status != "active" {
				response.Status = "issues"
				break
			}
		}
	}
	return response
}

func printSessionGroupResponse(out, errOut io.Writer, response SessionGroupResponse, asJSON bool) int {
	if asJSON {
		if err := writeJSONTo(out, response); err != nil {
			fmt.Fprintln(errOut, "heimdal:", err)
			return 1
		}
	} else {
		fmt.Fprintf(out, "Heimdal session group %s: %s", response.Group, response.Status)
		if response.URL != "" {
			fmt.Fprintf(out, " (%s)", response.URL)
		}
		fmt.Fprintln(out)
		for _, actor := range response.Actors {
			owner := ""
			if actor.Owner {
				owner = ", server owner"
			}
			fmt.Fprintf(out, "  %s: %s%s\n", actor.Name, actor.Status, owner)
		}
		if response.Error != "" {
			fmt.Fprintln(errOut, "heimdal:", response.Error)
		}
	}
	if response.Status == "failed" || response.Status == "issues" || response.Status == "error" {
		return 1
	}
	return 0
}
