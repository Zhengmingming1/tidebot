//go:build windows

package diskspace

import "golang.org/x/sys/windows"

func of(path string) (Info, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Info{}, err
	}
	// "free to caller" honours per-user quotas, like Bavail on Unix.
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return Info{}, err
	}
	return Info{Free: free, Total: total}, nil
}
