package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

var errProcessGroupsUnsupported = errors.New("process groups are unsupported")

const managedProcessExitGrace = 3 * time.Second

func execCommandContext(ctx context.Context, dir string, command []string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

func configureManagedProcess(cmd *exec.Cmd) {
	configureDetachedProcess(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		stopDetachedProcess(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = managedProcessExitGrace
}

func waitManagedProcess(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("managed process is nil")
	}
	err := cmd.Wait()
	if cmd.Process == nil {
		return err
	}
	killDetachedProcess(cmd.Process.Pid)
	return err
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

func killDetachedProcess(pid int) {
	if pid <= 0 {
		return
	}
	if err := killProcessGroup(pid); err == nil {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}

func managedProcessPID(argument string) int {
	if argument == "" {
		return 0
	}
	return processPIDWithArgument(argument)
}
