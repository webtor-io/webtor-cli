//go:build windows

package picker

// Windows consoles have no VMIN/VTIME; live screens degrade to redrawing
// only on keystrokes there.
func setPollRead(fd int) bool    { return false }
func setInstantRead(fd int) bool { return false }
