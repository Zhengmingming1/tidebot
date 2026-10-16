//go:build !linux && !darwin && !windows

package runner

import "errors"

func procTable() (map[int]ProcInfo, error) { return nil, errors.New("unsupported") }
