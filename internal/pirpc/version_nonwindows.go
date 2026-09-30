//go:build !windows

package pirpc

import "os/exec"

func hideVersionWindow(cmd *exec.Cmd) {}
