package cli

import (
	"context"
	"os/exec"
)

func execCommandContext(ctx context.Context, dir string, command []string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}
