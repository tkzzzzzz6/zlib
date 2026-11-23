//go:build !windows

package cli

import (
	"os/exec"
	"runtime"
)

func launchBookReader(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Run()
	}
	// xdg-open can stay attached until the reader quits. Reap it without
	// delaying the bookshelf update or tying the reader to this TUI.
	cmd := exec.Command("xdg-open", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
