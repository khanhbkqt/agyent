//go:build windows

package agy

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modkernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformationStruct struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// ProcessJobGuard binds child and grandchild processes to a Windows Kernel Job Object
// so that when the guard is closed, the Windows kernel automatically terminates all child processes.
type ProcessJobGuard struct {
	jobHandle syscall.Handle
}

// CreateProcessJobGuard constructs a Windows Job Object with KILL_ON_JOB_CLOSE limit.
func CreateProcessJobGuard() (*ProcessJobGuard, error) {
	handle, _, err := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}
	hJob := syscall.Handle(handle)

	var info jobObjectExtendedLimitInformationStruct
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose

	ret, _, err := procSetInformationJobObject.Call(
		uintptr(hJob),
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)
	if ret == 0 {
		_ = syscall.CloseHandle(hJob)
		return nil, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}

	return &ProcessJobGuard{jobHandle: hJob}, nil
}

// AttachProcess assigns a running process to the Job Object.
func (g *ProcessJobGuard) AttachProcess(process *os.Process) error {
	if g == nil || g.jobHandle == 0 || process == nil {
		return nil
	}
	hProc, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(process.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess failed for PID %d: %w", process.Pid, err)
	}
	defer syscall.CloseHandle(hProc)

	ret, _, err := procAssignProcessToJobObject.Call(uintptr(g.jobHandle), uintptr(hProc))
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject failed for PID %d: %w", process.Pid, err)
	}
	return nil
}

// Close releases the Job Object handle, immediately terminating any remaining child processes.
func (g *ProcessJobGuard) Close() error {
	if g != nil && g.jobHandle != 0 {
		err := syscall.CloseHandle(g.jobHandle)
		g.jobHandle = 0
		return err
	}
	return nil
}
