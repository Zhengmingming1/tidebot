//go:build windows

package runner

import (
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupProcess starts the child in a new process group; afterStart assigns it to a job
// object with KILL_ON_JOB_CLOSE so cancel/exit takes down the whole tree.
// The job object ends the tree at once; grace isn't used (no sandbox on Windows).
func setupProcess(cmd *exec.Cmd, grace time.Duration) func() {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	return func() {}
}

func afterStart(cmd *exec.Cmd) func() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, _ = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		_ = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
	}
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	return func() { _ = windows.CloseHandle(job) }
}
