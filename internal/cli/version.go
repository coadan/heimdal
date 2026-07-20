package cli

import (
	"runtime/debug"
	"strings"
)

func heimdalVersion() string {
	version := "devel"
	revision := ""
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	if revision != "" {
		return "heimdal " + version + " (" + revision + ")"
	}
	return "heimdal " + strings.TrimSpace(version)
}
