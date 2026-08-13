//go:build !windows

package picker

import (
	"golang.org/x/sys/unix"
)

// setInstantRead makes Read return immediately with whatever is buffered
// (VMIN=0/VTIME=0) — used to flush leftover terminal reports without any
// wait.
func setInstantRead(fd int) bool {
	t, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return false
	}
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(fd, ioctlWriteTermios, t) == nil
}

// setPollRead switches the raw terminal to polling reads: VMIN=0/VTIME=1
// makes Read return within ~100ms even with no input, which is what lets a
// live screen redraw between keystrokes. Returns false when the tweak is
// unavailable — the caller then falls back to a static screen.
func setPollRead(fd int) bool {
	t, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return false
	}
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 1
	return unix.IoctlSetTermios(fd, ioctlWriteTermios, t) == nil
}
