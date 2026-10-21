//go:build !unix && !windows

package diskspace

func of(string) (Info, error) { return Info{}, ErrUnsupported }
