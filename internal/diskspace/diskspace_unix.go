//go:build unix

package diskspace

import "golang.org/x/sys/unix"

func of(path string) (Info, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Info{}, err
	}
	// Bsize is int64 on Linux and uint32 on macOS; both convert cleanly.
	bs := uint64(st.Bsize)
	return Info{Free: uint64(st.Bavail) * bs, Total: uint64(st.Blocks) * bs}, nil
}
