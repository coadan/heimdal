package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
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

type managedProcess struct {
	cmd   *exec.Cmd
	mu    sync.Mutex
	owned map[int]struct{}
}

func configureManagedProcess(cmd *exec.Cmd) *managedProcess {
	process := &managedProcess{
		cmd:   cmd,
		owned: map[int]struct{}{},
	}
	configureDetachedProcess(cmd)
	cmd.Cancel = func() error {
		process.stop()
		return nil
	}
	cmd.WaitDelay = managedProcessExitGrace
	return process
}

func (process *managedProcess) wait() error {
	if process == nil || process.cmd == nil {
		return errors.New("managed process is nil")
	}
	err := process.cmd.Wait()
	process.kill()
	return err
}

func (process *managedProcess) stop() {
	for _, pid := range process.capture() {
		stopDetachedProcess(pid)
	}
}

func (process *managedProcess) kill() {
	for _, pid := range process.ownedPIDs() {
		killDetachedProcess(pid)
	}
}

func (process *managedProcess) capture() []int {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return nil
	}
	pids := processTreePIDs(process.cmd.Process.Pid)
	process.mu.Lock()
	defer process.mu.Unlock()
	for _, pid := range pids {
		process.owned[pid] = struct{}{}
	}
	return reversePIDs(pids)
}

func (process *managedProcess) ownedPIDs() []int {
	process.mu.Lock()
	defer process.mu.Unlock()
	pids := make([]int, 0, len(process.owned))
	for pid := range process.owned {
		pids = append(pids, pid)
	}
	return pids
}

func reversePIDs(pids []int) []int {
	for left, right := 0, len(pids)-1; left < right; left, right = left+1, right-1 {
		pids[left], pids[right] = pids[right], pids[left]
	}
	return pids
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
