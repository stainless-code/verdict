//go:build windows

package exec

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	// createSuspended is CREATE_SUSPENDED: the primary thread starts
	// suspended and runs no user-mode code until it is resumed.
	createSuspended = 0x00000004
	// th32csSnapThread is TH32CS_SNAPTHREAD.
	th32csSnapThread = 0x00000004
	// threadSuspendResume is THREAD_SUSPEND_RESUME, enough for ResumeThread.
	threadSuspendResume = 0x0002
	// resumeFailed is ResumeThread's (DWORD)-1 failure return.
	resumeFailed = 0xFFFFFFFF
	// jobObjectExtendedLimitInformation is JOBOBJECTINFOCLASS 9.
	jobObjectExtendedLimitInformation = 9
	// jobObjectBasicAccountingInformation is JOBOBJECTINFOCLASS 1.
	jobObjectBasicAccountingInformation = 1
	// jobLimitKillOnJobClose terminates survivors when the last job handle
	// closes (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE).
	jobLimitKillOnJobClose = 0x2000
	// processQueryLimitedInformation lets us read the exit code.
	processQueryLimitedInformation = 0x1000
	// processSetQuota is required by AssignProcessToJobObject alongside
	// PROCESS_TERMINATE.
	processSetQuota = 0x0100
	// stillActive is STILL_ACTIVE: the process has not exited.
	stillActive = 259
	// forcedExitCode is the exit status TerminateJobObject records.
	forcedExitCode = 1
)

var (
	procCreateJobObject         = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateJobObjectW")
	procSetInformationJobObject = syscall.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject")
	procAssignProcessToJob      = syscall.NewLazyDLL("kernel32.dll").NewProc("AssignProcessToJobObject")
	procTerminateJobObject      = syscall.NewLazyDLL("kernel32.dll").NewProc("TerminateJobObject")
	procQueryInformationJob     = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryInformationJobObject")
	procGetExitCodeProcess      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetExitCodeProcess")
	procThread32First           = syscall.NewLazyDLL("kernel32.dll").NewProc("Thread32First")
	procThread32Next            = syscall.NewLazyDLL("kernel32.dll").NewProc("Thread32Next")
	procOpenThread              = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenThread")
	procResumeThread            = syscall.NewLazyDLL("kernel32.dll").NewProc("ResumeThread")
)

type threadEntry struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

type jobExtendedLimit struct {
	BasicLimit            jobBasicLimit
	IOInfo                jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobBasicLimit struct {
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

type jobIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

type tree struct {
	mu        sync.Mutex
	handle    syscall.Handle
	attached  bool
	closed    bool
	suspended bool
}

var trees sync.Map

func setGroup(cmd *osexec.Cmd) {
	tr := &tree{}
	handle, _, _ := procCreateJobObject.Call(0, 0)
	if handle != 0 {
		info := jobExtendedLimit{}
		info.BasicLimit.LimitFlags = jobLimitKillOnJobClose
		ok, _, _ := procSetInformationJobObject.Call(
			handle, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info),
		)
		if ok != 0 {
			tr.handle = syscall.Handle(handle)
			tr.suspended = true
			if cmd.SysProcAttr == nil {
				cmd.SysProcAttr = &syscall.SysProcAttr{}
			}
			cmd.SysProcAttr.CreationFlags |= createSuspended
		} else {
			_ = syscall.CloseHandle(syscall.Handle(handle)) // creation failed; no tree to kill
		}
	}
	trees.Store(cmd, tr)
}

func treeFor(cmd *osexec.Cmd) *tree {
	v, _ := trees.Load(cmd)
	tr, _ := v.(*tree)
	return tr
}

func (tr *tree) attach(cmd *osexec.Cmd) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.handle == 0 {
		return errNoJob
	}
	if tr.attached {
		return nil
	}
	if cmd.Process == nil {
		return errNoProcess
	}
	handle, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return watchdogError("open verify process for job assignment: " + err.Error())
	}
	defer func() { _ = syscall.CloseHandle(handle) }() // assignment error already rules
	r, _, callErr := procAssignProcessToJob.Call(uintptr(tr.handle), uintptr(handle))
	if r == 0 {
		return watchdogError("assign verify process to job object: " + callErr.Error())
	}
	tr.attached = true
	return nil
}

func armStarted(cmd *osexec.Cmd) error {
	tr := treeFor(cmd)
	if tr == nil || !tr.suspended {
		return nil
	}
	err := tr.attach(cmd)
	if err == nil {
		err = resumeProcess(uint32(cmd.Process.Pid))
	}
	if err != nil {
		_ = cmd.Process.Kill() // best effort: a suspended child must not leak
		_ = cmd.Wait()         // reap it and release the output copiers
		return err
	}
	return nil
}

// resumeProcess resumes the suspended primary thread of pid. os/exec closes
// the primary thread handle CreateProcess returns, so the thread is found by
// enumerating a Toolhelp thread snapshot for pid's threads. That is the
// documented route (NtResumeProcess is not): a CREATE_SUSPENDED process has
// exactly one thread, suspend count 1, and the caller still holds the
// process handle, so the pid cannot have been recycled and ResumeThread once
// per owned thread leaves it running.
func resumeProcess(pid uint32) error {
	snap, err := syscall.CreateToolhelp32Snapshot(th32csSnapThread, 0)
	if err != nil {
		return watchdogError("snapshot verify process threads: " + err.Error())
	}
	defer func() { _ = syscall.CloseHandle(snap) }() // snapshot read already rules
	resumed := 0
	for _, id := range threadsOf(snap, pid) {
		if resumeThread(id) {
			resumed++
		}
	}
	if resumed == 0 {
		return errResumeFailed
	}
	return nil
}

func threadsOf(snap syscall.Handle, pid uint32) []uint32 {
	var ids []uint32
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	more, _, _ := procThread32First.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	for more != 0 {
		if entry.OwnerProcessID == pid {
			ids = append(ids, entry.ThreadID)
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		more, _, _ = procThread32Next.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	}
	return ids
}

func resumeThread(id uint32) bool {
	th, _, _ := procOpenThread.Call(threadSuspendResume, 0, uintptr(id))
	if th == 0 {
		return false
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(th)) }() // resume result already rules
	prev, _, _ := procResumeThread.Call(th)
	return uint32(prev) != resumeFailed
}

func (tr *tree) terminate() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.handle == 0 {
		return false
	}
	r, _, _ := procTerminateJobObject.Call(uintptr(tr.handle), forcedExitCode)
	return r != 0
}

func (tr *tree) active() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.handle == 0 {
		return false
	}
	var info jobAccounting
	r, _, _ := procQueryInformationJob.Call(
		uintptr(tr.handle), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0,
	)
	return r != 0 && info.ActiveProcesses > 0
}

func killGroup(cmd *osexec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	if tr := treeFor(cmd); tr != nil && tr.attach(cmd) == nil && tr.terminate() {
		return nil
	}
	return taskkillTree(cmd.Process.Pid)
}

func taskkillTree(pid int) error {
	kill := osexec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := kill.CombinedOutput(); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("taskkill verify tree: %v\n%s", err, out)
	}
	return nil
}

func processAlive(_ context.Context, cmd *osexec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(cmd.Process.Pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(handle) }() // liveness read already rules
	var code uint32
	r, _, _ := procGetExitCodeProcess.Call(uintptr(handle), uintptr(unsafe.Pointer(&code)))
	return r != 0 && code == stillActive
}

func sweepOrphans(cmd *osexec.Cmd) bool {
	tr := treeFor(cmd)
	if tr == nil || !tr.active() {
		return false
	}
	time.Sleep(sweepGrace)
	if !tr.active() {
		return false
	}
	tr.terminate()
	return true
}

func TrimTail(s string) string {
	return strings.TrimRight(s, "\r\n")
}

func releaseTree(cmd *osexec.Cmd) {
	v, ok := trees.LoadAndDelete(cmd)
	if !ok {
		return
	}
	tr, _ := v.(*tree)
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed {
		return
	}
	tr.closed = true
	if tr.handle != 0 {
		_ = syscall.CloseHandle(tr.handle) // final kill-on-close; no recovery path
		tr.handle = 0
	}
}

func waitStatusOf(exitErr *osexec.ExitError) (waitStatus, bool) {
	if exitErr.ProcessState == nil {
		return waitStatus{}, false
	}
	return waitStatus{status: exitErr.ProcessState.ExitCode()}, true
}

func timeoutExitCode() (int, bool) {
	return sigkillExit, true
}

func havePS() bool {
	return true
}

type waitStatus struct {
	signal   int
	status   int
	signaled bool
}

var errNoProcess = watchdogError("no verify process")

var errNoJob = watchdogError("no verify job object")

var errResumeFailed = watchdogError("resume suspended verify process: no thread resumed")

type watchdogError string

func (e watchdogError) Error() string { return string(e) }

const sweepGrace = 2 * time.Second

const waitDelay = 2 * time.Second
