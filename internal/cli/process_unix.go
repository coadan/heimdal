//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func terminateProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func processPIDWithArgument(argument string) int {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, argument) {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err == nil && pid != os.Getpid() {
			return pid
		}
	}
	return 0
}
