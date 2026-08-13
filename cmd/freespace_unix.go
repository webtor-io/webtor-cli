//go:build !windows

package cmd

import "golang.org/x/sys/unix"

// freeSpace reports the bytes available to this user on dir's filesystem,
// or 0 when that cannot be determined.
func freeSpace(dir string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
