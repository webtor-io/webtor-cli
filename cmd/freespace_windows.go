//go:build windows

package cmd

// freeSpace is not wired on Windows; the folder check simply omits the
// free-space line there.
func freeSpace(dir string) int64 { return 0 }
