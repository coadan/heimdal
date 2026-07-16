package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRunOptionsPreservesPlaywrightArgs(t *testing.T) {
	options, err := parseRunOptions([]string{
		"--root", "/tmp/project",
		"--run-id", "branch/run",
		"--headed",
		"tests/example.spec.ts",
		"--grep", "victory",
		"--",
		"--project=chromium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.RunID != "branch/run" || !options.Headed {
		t.Fatalf("unexpected options: %#v", options)
	}
	want := []string{"tests/example.spec.ts", "--grep", "victory", "--project=chromium"}
	if strings.Join(options.Forwarded, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forwarded args = %#v, want %#v", options.Forwarded, want)
	}
}

func TestRunEnvironmentUsesConfiguredIsolationNames(t *testing.T) {
	project := Project{
		Root:   "/tmp/project",
		Branch: "codex/test",
		Config: Config{Playwright: PlaywrightConfig{
			RunIDEnv: "VOID_RUN_ID",
			PortEnv:  "VOID_PORT",
			Env: map[string]string{
				"VOID_ARTIFACTS": "${RUN_DIR}",
			},
		}},
	}
	env := strings.Join(runEnvironment(project, "run-1", "/tmp/run-1", "/tmp/run-1/test-results", "/tmp/run-1/report", 4567), "\n")
	for _, expected := range []string{
		"HEIMDAL_RUN_ID=run-1",
		"VOID_RUN_ID=run-1",
		"VOID_PORT=4567",
		"VOID_ARTIFACTS=/tmp/run-1",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("environment did not contain %q:\n%s", expected, env)
		}
	}
}

func TestWriteProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".heimdal.json")
	cfg := defaultConfig("playwright.config.ts")
	if err := writeProjectConfig(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Playwright.Config != "playwright.config.ts" || decoded.Artifacts.Directory != defaultArtifactDir {
		t.Fatalf("unexpected config: %#v", decoded)
	}
}

func TestRunHelpIsAvailableWithoutAProject(t *testing.T) {
	var out strings.Builder
	if code := Run(context.Background(), []string{"help"}, &out, io.Discard); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(out.String(), "heimdal run") {
		t.Fatalf("help output did not mention run: %s", out.String())
	}
}

func TestRunResultJSONIsStable(t *testing.T) {
	result := RunResult{SchemaVersion: 1, RunID: "run-1", Status: "passed", Artifacts: Artifacts{RunDir: "/tmp/run-1"}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"schema_version":1`) {
		t.Fatalf("unexpected result JSON: %s", encoded)
	}
}

func TestInstallSkillMaterializesEmbeddedFiles(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "skills", "heimdal-playwright-qa")
	if err := installSkill(destination, false); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"SKILL.md", "agents/openai.yaml"} {
		if _, err := os.Stat(filepath.Join(destination, relative)); err != nil {
			t.Fatalf("installed skill is missing %s: %v", relative, err)
		}
	}
	if err := installSkill(destination, false); err == nil {
		t.Fatal("second install should preserve the existing skill unless forced")
	}
}
