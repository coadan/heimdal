//go:build !windows

package cli

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedProcessPIDFindsExactOwnedArgument(t *testing.T) {
	argument := "heimdal-owned-process-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done", argument)
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killDetachedProcess(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	if pid := managedProcessPID(argument); pid != cmd.Process.Pid {
		t.Fatalf("managed process pid = %d, want %d", pid, cmd.Process.Pid)
	}
}

func TestManagedProcessCancellationReapsDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `
		trap 'exit 0' TERM
		(
			trap '' TERM
			while :; do sleep 1; done
		) &
		printf '%s %s\n' "$$" "$!"
		while :; do sleep 1; done
	`)
	configureManagedProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killDetachedProcess(cmd.Process.Pid)
	})

	pids := make(chan []int, 1)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() {
			readErr <- errors.New("managed process did not publish descendant pids")
			return
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			readErr <- errors.New("managed process published malformed descendant pids")
			return
		}
		parsed := make([]int, 0, len(fields))
		for _, field := range fields {
			pid, parseErr := strconv.Atoi(field)
			if parseErr != nil {
				readErr <- parseErr
				return
			}
			parsed = append(parsed, pid)
		}
		pids <- parsed
	}()

	var processPIDs []int
	select {
	case processPIDs = <-pids:
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed process readiness")
	}

	cancel()
	if err := waitManagedProcess(cmd); err == nil {
		t.Fatal("cancelled managed process unexpectedly succeeded")
	}
	for _, pid := range processPIDs {
		deadline := time.Now().Add(time.Second)
		for {
			err := syscall.Kill(pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("process %d survived managed cancellation: %v", pid, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
