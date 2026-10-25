//go:build !linux && !darwin && !windows

package power

func read() State { return Unknown }
