//go:build !windows

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedProcessHelper(t *testing.T) {
	role := os.Getenv("HEIMDAL_MANAGED_PROCESS_HELPER")
	if role == "" {
		t.Skip("helper subprocess only")
	}
	if role == "child" {
		signal.Ignore(syscall.SIGTERM)
		select {}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestManagedProcessHelper$")
	child.Env = append(os.Environ(), "HEIMDAL_MANAGED_PROCESS_HELPER=child")
	configureDetachedProcess(child)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%d %d\n", os.Getpid(), child.Process.Pid)
	select {}
}

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

func TestManagedProcessCancellationReapsSeparateDescendantGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagedProcessHelper$")
	cmd.Env = append(os.Environ(), "HEIMDAL_MANAGED_PROCESS_HELPER=parent")
	managed := configureManagedProcess(cmd)
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

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("managed helper did not publish descendant pids")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) != 2 {
		t.Fatalf("managed helper published malformed descendant pids: %q", scanner.Text())
	}
	processPIDs := make([]int, 0, len(fields))
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
		processPIDs = append(processPIDs, pid)
	}

	cancel()
	if err := managed.wait(); err == nil {
		t.Fatal("cancelled managed process unexpectedly succeeded")
	}
	assertProcessesGone(t, processPIDs)
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
	managed := configureManagedProcess(cmd)
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
	if err := managed.wait(); err == nil {
		t.Fatal("cancelled managed process unexpectedly succeeded")
	}
	assertProcessesGone(t, processPIDs)
}

func assertProcessesGone(t *testing.T, processPIDs []int) {
	t.Helper()
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
