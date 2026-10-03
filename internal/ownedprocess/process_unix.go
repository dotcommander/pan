//go:build !windows

package ownedprocess

import (
	"errors"
	"os/exec"
	"syscall"
)

func launch(cmd *exec.Cmd) (func() error, func(), error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}, func() {}, nil
}
