package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionGroupSharesURLRoutesActorAndStopsOwnerLastOnce(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	server := httptest.NewServer(nil)
	defer server.Close()
	project, runnerCalls, serverCalls := sessionGroupFixture(t, server.URL, "")

	group, path, err := startSessionGroup(context.Background(), project, sessionGroupOptions{
		SessionOptions: SessionOptions{RunID: "group-run"},
		Actors:         []string{"host", "guest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stopSessionGroupActors(context.Background(), project, group)
	})
	if group.Status != "active" || group.Name != defaultSessionName || group.URL != server.URL {
		t.Fatalf("group = %#v", group)
	}
	if want := filepath.Join(project.Root, defaultArtifactDir, "sessions", "groups", "default.json"); path != want {
		t.Fatalf("group path = %q, want %q", path, want)
	}
	host, err := readSessionState(group.Actors[0].StatePath)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := readSessionState(group.Actors[1].StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if host.URL != server.URL || guest.URL != host.URL || host.Port == 0 || guest.Port != host.Port || group.Port != host.Port {
		t.Fatalf("actor endpoints: group=%s:%d host=%s:%d guest=%s:%d", group.URL, group.Port, host.URL, host.Port, guest.URL, guest.Port)
	}
	if host.ServerPID <= 0 || guest.ServerPID != 0 {
		t.Fatalf("server ownership: host pid=%d guest pid=%d", host.ServerPID, guest.ServerPID)
	}
	if lines := nonEmptyLines(t, serverCalls); len(lines) != 1 {
		t.Fatalf("server starts = %d, want 1: %v", len(lines), lines)
	}
	var statusOut, statusErr strings.Builder
	if code := runSessionGroupStatus([]string{"--dir", project.Root, "--json"}, &statusOut, &statusErr); code != 0 {
		t.Fatalf("group status exit = %d\nstdout=%s\nstderr=%s", code, statusOut.String(), statusErr.String())
	}
	var status SessionGroupResponse
	if err := json.Unmarshal([]byte(statusOut.String()), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "active" || len(status.Actors) != 2 || status.Actors[0].Server != "running" || status.Actors[1].Server != "" {
		t.Fatalf("group status = %#v", status)
	}

	var out, errOut strings.Builder
	code := runSessionAction(context.Background(), "click", []string{"--actor", "Guest", "e12", "--dir", project.Root, "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("actor click exit = %d\nstdout=%s\nstderr=%s", code, out.String(), errOut.String())
	}
	var action SessionResponse
	if err := json.Unmarshal([]byte(out.String()), &action); err != nil {
		t.Fatal(err)
	}
	if action.Actor != "guest" || action.Group != defaultSessionName || action.Session != guest.Name {
		t.Fatalf("routed action = %#v", action)
	}
	callsBeforeStop := nonEmptyLines(t, runnerCalls)
	if !strings.Contains(callsBeforeStop[len(callsBeforeStop)-1], guest.Name+"|") || !strings.Contains(callsBeforeStop[len(callsBeforeStop)-1], " click ") {
		t.Fatalf("click was not routed to guest: %v", callsBeforeStop)
	}

	out.Reset()
	errOut.Reset()
	if code := runSessionGroupStop(context.Background(), []string{"--dir", project.Root, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("group stop exit = %d\nstdout=%s\nstderr=%s", code, out.String(), errOut.String())
	}
	afterFirstStop := nonEmptyLines(t, runnerCalls)
	if len(afterFirstStop) != len(callsBeforeStop)+2 {
		t.Fatalf("stop calls = %v", afterFirstStop)
	}
	if !strings.Contains(afterFirstStop[len(afterFirstStop)-2], guest.Name+"|") || !strings.HasSuffix(afterFirstStop[len(afterFirstStop)-2], " close") {
		t.Fatalf("non-owner was not stopped first: %v", afterFirstStop)
	}
	if !strings.Contains(afterFirstStop[len(afterFirstStop)-1], host.Name+"|") || !strings.HasSuffix(afterFirstStop[len(afterFirstStop)-1], " close") {
		t.Fatalf("owner was not stopped last: %v", afterFirstStop)
	}
	out.Reset()
	errOut.Reset()
	if code := runSessionGroupStop(context.Background(), []string{"--dir", project.Root, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("second group stop exit = %d: %s %s", code, out.String(), errOut.String())
	}
	if afterSecondStop := nonEmptyLines(t, runnerCalls); len(afterSecondStop) != len(afterFirstStop) {
		t.Fatalf("second stop closed actors again: before=%v after=%v", afterFirstStop, afterSecondStop)
	}
}

func TestSessionGroupFailedStartCleansAlreadyStartedActors(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	server := httptest.NewServer(nil)
	defer server.Close()
	failingGuest := sessionGroupActorName(defaultSessionName, "guest")
	project, runnerCalls, _ := sessionGroupFixture(t, server.URL, failingGuest)

	group, path, err := startSessionGroup(context.Background(), project, sessionGroupOptions{
		SessionOptions: SessionOptions{RunID: "failed-run"},
		Actors:         []string{"host", "guest"},
	})
	if err == nil {
		t.Fatal("group start unexpectedly succeeded")
	}
	if group.Status != "failed" || group.StoppedAt == nil || !strings.Contains(group.Error, "guest") {
		t.Fatalf("failed group = %#v, err=%v", group, err)
	}
	persisted, readErr := readSessionGroup(path)
	if readErr != nil || persisted.Status != "failed" {
		t.Fatalf("persisted failed group = %#v, %v", persisted, readErr)
	}
	host, readErr := readSessionState(group.Actors[0].StatePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if host.StoppedAt == nil {
		t.Fatalf("owner remained active after partial start: %#v", host)
	}
	calls := nonEmptyLines(t, runnerCalls)
	if len(calls) != 4 || !strings.Contains(calls[0], "host") || !strings.Contains(calls[1], "guest") || !strings.Contains(calls[len(calls)-1], "host") || !strings.HasSuffix(calls[len(calls)-1], " close") {
		t.Fatalf("partial-start cleanup calls = %v", calls)
	}
}

func TestSessionGroupStartRecoversPersistedPartialGroup(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	server := httptest.NewServer(nil)
	defer server.Close()
	project, runnerCalls, _ := sessionGroupFixture(t, server.URL, "")
	hostSession := sessionGroupActorName(defaultSessionName, "host")
	runDir := filepath.Join(project.Root, defaultArtifactDir, "sessions", hostSession, "partial-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostState := SessionState{SchemaVersion: 1, Name: hostSession, Group: defaultSessionName, Actor: "host", RunID: "partial-run", Root: project.Root, SessionDir: runDir, CLIConfig: filepath.Join(runDir, "playwright-cli.json"), ActionLog: filepath.Join(runDir, "actions.jsonl"), URL: server.URL, StartedAt: time.Now().UTC()}
	statePath := filepath.Join(filepath.Dir(runDir), "session.json")
	if err := writeSessionState(statePath, hostState); err != nil {
		t.Fatal(err)
	}
	partial := SessionGroupState{SchemaVersion: 1, Name: defaultSessionName, RunID: "partial", Root: project.Root, URL: server.URL, Status: "starting", StartedAt: hostState.StartedAt, Actors: []SessionGroupActor{
		{Name: "host", Session: hostSession, StatePath: statePath, Owner: true, Started: true},
		{Name: "guest", Session: sessionGroupActorName(defaultSessionName, "guest"), StatePath: filepath.Join(project.Root, defaultArtifactDir, "sessions", sessionGroupActorName(defaultSessionName, "guest"), "session.json")},
	}}
	if err := writeSessionGroup(sessionGroupIndexPath(project, "", defaultSessionName), partial); err != nil {
		t.Fatal(err)
	}

	group, _, err := startSessionGroup(context.Background(), project, sessionGroupOptions{SessionOptions: SessionOptions{RunID: "recovered"}, Actors: []string{"host", "guest"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stopSessionGroupActors(context.Background(), project, group) })
	if group.Status != "active" || !group.Actors[0].Started || !group.Actors[1].Started {
		t.Fatalf("recovered group = %#v", group)
	}
	calls := nonEmptyLines(t, runnerCalls)
	if len(calls) < 3 || !strings.Contains(calls[0], hostSession+"|") || !strings.HasSuffix(calls[0], " close") {
		t.Fatalf("partial owner was not recovered before restart: %v", calls)
	}
}

func TestSessionGroupTimelineAndReportMergeActorActionsByTime(t *testing.T) {
	root := t.TempDir()
	config := defaultConfig("")
	writeSessionGroupConfig(t, root, config)
	project := Project{Root: root, Branch: "main", Config: config, ConfigFile: filepath.Join(root, configFileName)}
	started := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	group := SessionGroupState{SchemaVersion: 1, Name: "party", RunID: "party-run", Root: root, URL: "http://127.0.0.1:4173", Status: "active", StartedAt: started}
	for index, actor := range []string{"host", "guest"} {
		sessionName := sessionGroupActorName(group.Name, actor)
		runDir := filepath.Join(root, defaultArtifactDir, "sessions", sessionName, "run-1")
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := SessionState{SchemaVersion: 1, Name: sessionName, Group: group.Name, Actor: actor, RunID: "run-1", Root: root, SessionDir: runDir, ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: started}
		statePath := filepath.Join(filepath.Dir(runDir), "session.json")
		if err := writeSessionState(statePath, state); err != nil {
			t.Fatal(err)
		}
		when := started.Add(time.Duration(2-index) * time.Second)
		if err := appendSessionAction(state.ActionLog, SessionActionRecord{Sequence: 1, StartedAt: when, FinishedAt: when.Add(time.Millisecond), Args: []string{"click", "e12"}}); err != nil {
			t.Fatal(err)
		}
		group.Actors = append(group.Actors, SessionGroupActor{Name: actor, Session: sessionName, StatePath: statePath, Owner: index == 0, Started: true})
	}
	path := sessionGroupIndexPath(project, "", group.Name)
	if err := writeSessionGroup(path, group); err != nil {
		t.Fatal(err)
	}

	var timelineOut, errOut strings.Builder
	if code := runSessionGroupTimeline([]string{"--dir", root, "--name", group.Name, "--json"}, &timelineOut, &errOut); code != 0 {
		t.Fatalf("timeline exit = %d: %s %s", code, timelineOut.String(), errOut.String())
	}
	var timeline SessionTimeline
	if err := json.Unmarshal([]byte(timelineOut.String()), &timeline); err != nil {
		t.Fatal(err)
	}
	if timeline.Actions != 2 || len(timeline.Entries) != 2 || timeline.Entries[0].Actor != "guest" || timeline.Entries[1].Actor != "host" {
		t.Fatalf("merged timeline = %#v", timeline)
	}
	if timeline.Entries[0].Sequence != 1 || timeline.Entries[0].ActorSequence != 1 {
		t.Fatalf("merged sequence = %#v", timeline.Entries)
	}

	var reportOut strings.Builder
	if code := runSessionGroupReport([]string{"--dir", root, "--name", group.Name, "--json"}, &reportOut, &errOut); code != 0 {
		t.Fatalf("report exit = %d: %s %s", code, reportOut.String(), errOut.String())
	}
	var report SessionReport
	if err := json.Unmarshal([]byte(reportOut.String()), &report); err != nil {
		t.Fatal(err)
	}
	if report.Group != group.Name || strings.Join(report.Actors, ",") != "host,guest" || report.Actions != 2 || report.Categories["interaction"] != 2 {
		t.Fatalf("merged report = %#v", report)
	}
}

func TestSessionGroupActorsAreBoundedSanitizedAndUnique(t *testing.T) {
	actors, err := parseSessionGroupActors(" Host User , Guest_User ")
	if err != nil || strings.Join(actors, ",") != "host-user,guest-user" {
		t.Fatalf("actors = %v, %v", actors, err)
	}
	for _, value := range []string{
		"host",
		"host,HOST",
		"host,guest,a,b,c,d,e,f,i",
		"host,---",
	} {
		if _, err := parseSessionGroupActors(value); err == nil {
			t.Fatalf("invalid actors %q were accepted", value)
		}
	}
}

func TestSessionGroupActorSelectionFlowsThroughCommonSessionOptions(t *testing.T) {
	wait, err := parseSessionWaitOptions([]string{"--role", "button", "--name", "Continue", "--actor", "guest", "--group", "party"})
	if err != nil {
		t.Fatal(err)
	}
	if wait.SessionOptions.Actor != "guest" || wait.SessionOptions.Group != "party" || wait.Name != "Continue" {
		t.Fatalf("wait options = %#v", wait)
	}
	expect, err := parseSessionExpectOptions([]string{"--text", "Saved", "--actor", "guest", "--group", "party"})
	if err != nil {
		t.Fatal(err)
	}
	if expect.SessionOptions.Actor != "guest" || expect.SessionOptions.Group != "party" {
		t.Fatalf("expect options = %#v", expect)
	}
	common, err := parseSessionOptions([]string{"--actor", "guest", "--group", "party", "e12"})
	if err != nil || common.Actor != "guest" || common.Group != "party" || strings.Join(common.Forwarded, ",") != "e12" {
		t.Fatalf("common options = %#v, %v", common, err)
	}
}

func TestSessionGroupActorSelectionRejectsAmbiguityAndAcceptsExplicitGroup(t *testing.T) {
	root := t.TempDir()
	config := defaultConfig("")
	writeSessionGroupConfig(t, root, config)
	project := Project{Root: root, Config: config}
	for _, name := range []string{"alpha", "beta"} {
		group := SessionGroupState{
			SchemaVersion: 1,
			Name:          name,
			Root:          root,
			Status:        "active",
			Actors:        []SessionGroupActor{{Name: "guest", Session: sessionGroupActorName(name, "guest"), Started: true}},
			StartedAt:     time.Now().UTC(),
		}
		if err := writeSessionGroup(sessionGroupIndexPath(project, "", name), group); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resolveSessionGroupActor(SessionOptions{Root: root, Actor: "guest"}); err == nil || !strings.Contains(err.Error(), "multiple active session groups") || !strings.Contains(err.Error(), "--group") {
		t.Fatalf("ambiguous actor selection error = %v", err)
	}
	resolved, err := resolveSessionGroupActor(SessionOptions{Root: root, Group: "beta", Actor: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != sessionGroupActorName("beta", "guest") || resolved.Actor != "" || resolved.Group != "" {
		t.Fatalf("explicit actor selection = %#v", resolved)
	}
}

func sessionGroupFixture(t *testing.T, url, failSession string) (Project, string, string) {
	t.Helper()
	root := t.TempDir()
	runnerCalls := filepath.Join(root, "runner-calls.log")
	serverCalls := filepath.Join(root, "server-calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	runnerScript := fmt.Sprintf(`#!/bin/sh
printf '%%s|%%s\n' "$HEIMDAL_SESSION_NAME" "$*" >> '%s'
mkdir -p "$HEIMDAL_SESSION_DIR"
printf '%%s\n' '- button "Save" [ref=e12]' > "$HEIMDAL_SESSION_DIR/page.yml"
if [ "$HEIMDAL_SESSION_NAME" = '%s' ]; then
  case " $* " in
    *" open "*) printf 'failed actor open\n' >&2; exit 7 ;;
  esac
fi
printf '%%s\n' '- [Snapshot]('"$HEIMDAL_SESSION_DIR"'/page.yml)'
`, runnerCalls, failSession)
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	serverRunner := filepath.Join(root, "fake-session-server")
	serverScript := fmt.Sprintf(`#!/bin/sh
printf 'started\n' >> '%s'
trap 'exit 0' TERM INT
while :; do sleep 1; done
`, serverCalls)
	if err := os.WriteFile(serverRunner, []byte(serverScript), 0o755); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig("")
	config.Session.Runner = []string{runner}
	config.Session.Command = []string{serverRunner}
	config.Session.URL = url
	config.Session.ServerTimeoutMS = 2000
	writeSessionGroupConfig(t, root, config)
	return Project{Root: root, Branch: "main", Config: config, ConfigFile: filepath.Join(root, configFileName), AgentRunner: []string{runner}}, runnerCalls, serverCalls
}

func writeSessionGroupConfig(t *testing.T, root string, config Config) {
	t.Helper()
	contents, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, configFileName), contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func nonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
