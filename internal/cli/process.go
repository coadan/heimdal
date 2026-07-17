package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

var errProcessGroupsUnsupported = errors.New("process groups are unsupported")

func execCommandContext(ctx context.Context, dir string, command []string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

func stopDetachedProcess(pid int) {
	if pid <= 0 {
		return
	}
	if err := terminateProcessGroup(pid); err == nil {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
