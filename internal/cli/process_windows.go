//go:build windows

package cli

import "os/exec"

func configureDetachedProcess(_ *exec.Cmd) {}

func terminateProcessGroup(_ int) error {
	return errProcessGroupsUnsupported
}
