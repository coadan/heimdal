//go:build windows

package cli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func configureDetachedProcess(_ *exec.Cmd) {}

func terminateProcessGroup(_ int) error {
	return errProcessGroupsUnsupported
}

func killProcessGroup(_ int) error {
	return errProcessGroupsUnsupported
}

func processPIDWithArgument(argument string) int {
	command := `$needle=$env:HEIMDAL_PROCESS_ARGUMENT; Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like "*$needle*" } | Select-Object -First 1 -ExpandProperty ProcessId`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Env = append(os.Environ(), "HEIMDAL_PROCESS_ARGUMENT="+argument)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid == os.Getpid() {
		return 0
	}
	return pid
}

func processTreePIDs(root int) []int {
	command := `Get-CimInstance Win32_Process | ForEach-Object { "$($_.ProcessId) $($_.ParentProcessId)" }`
	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command).Output()
	if err != nil {
		return []int{root}
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil {
			children[parent] = append(children[parent], pid)
		}
	}
	result := []int{root}
	for index := 0; index < len(result); index++ {
		result = append(result, children[result[index]]...)
	}
	return result
}
