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

// win32Call runs one Win32 proc and returns its first result word. A proc
// reports failure through that word — a NULL handle, FALSE, or (DWORD)-1 —
// and its third Call return is GetLastError, meaningful only after a failure.
// Callers read the word with win32OK, resumeOK, or a NULL-handle test.
func win32Call(proc *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := proc.Call(args...) //nolint:errcheck // GetLastError, ruled by the result word
	return r
}

// win32OK reads a BOOL result. BOOL is a 32-bit int and the x64 ABI leaves
// the upper half of the return register undefined, so only the low 32 bits
// carry the verdict.
func win32OK(r uintptr) bool {
	return uint32(r) != 0 //nolint:gosec // G115: the 32-bit narrowing is the contract
}

// resumeOK reads ResumeThread's DWORD result: only (DWORD)-1 fails, any other
// value is the thread's previous suspend count. Reading the full word would
// call a failure with junk in the upper half a resumed thread.
func resumeOK(r uintptr) bool {
	return uint32(r) != resumeFailed //nolint:gosec // G115: the 32-bit narrowing is the contract
}

// closeHandle releases a handle whose operation has already returned its
// verdict. A failed close is not actionable: the handle is closed or leaked
// to process exit, where the job object's kill-on-close still runs.
func closeHandle(h syscall.Handle) {
	_ = syscall.CloseHandle(h) //nolint:errcheck // cleanup; the caller's outcome already rules
}

// pid32 narrows a process id to the DWORD a Win32 call takes. A Windows
// process id is a DWORD, so the int already holds a value that fits.
func pid32(pid int) uint32 {
	return uint32(pid) //nolint:gosec // G115: Windows pids are 32-bit; os keeps one in an int
}

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

// jobBasicLimit mirrors JOBOBJECT_BASIC_LIMIT_INFORMATION. The field order
// is the Win32 ABI SetInformationJobObject reads, so it cannot be reordered
// to save padding.
type jobBasicLimit struct { //nolint:govet // fieldalignment: the Win32 ABI fixes the field order
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
	handle := win32Call(procCreateJobObject, 0, 0)
	if handle != 0 {
		info := jobExtendedLimit{}
		info.BasicLimit.LimitFlags = jobLimitKillOnJobClose
		ok := win32Call(procSetInformationJobObject,
			handle, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info),
		)
		if win32OK(ok) {
			tr.handle = syscall.Handle(handle)
			tr.suspended = true
			if cmd.SysProcAttr == nil {
				cmd.SysProcAttr = &syscall.SysProcAttr{}
			}
			cmd.SysProcAttr.CreationFlags |= createSuspended
		} else {
			closeHandle(syscall.Handle(handle)) // creation failed; no tree to kill
		}
	}
	trees.Store(cmd, tr)
}

func treeFor(cmd *osexec.Cmd) *tree {
	v, ok := trees.Load(cmd)
	if !ok {
		return nil
	}
	tr, ok := v.(*tree)
	if !ok {
		return nil
	}
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
	handle, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE, false, pid32(cmd.Process.Pid))
	if err != nil {
		return watchdogError("open verify process for job assignment: " + err.Error())
	}
	defer closeHandle(handle) // the assignment error already rules
	r, _, callErr := procAssignProcessToJob.Call(uintptr(tr.handle), uintptr(handle))
	if !win32OK(r) {
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
		err = resumeProcess(pid32(cmd.Process.Pid))
	}
	if err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			// A failed kill leaves the suspended child alive; returning without
			// Wait would hang on a process that can never run.
			return watchdogError(err.Error() + "; kill suspended verify process: " + killErr.Error())
		}
		_ = cmd.Wait() //nolint:errcheck // reap it and release the output copiers; the kill's exit is expected
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
	defer closeHandle(snap) // the snapshot read already rules
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
	more := win32Call(procThread32First, uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	for win32OK(more) {
		if entry.OwnerProcessID == pid {
			ids = append(ids, entry.ThreadID)
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		more = win32Call(procThread32Next, uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	}
	return ids
}

func resumeThread(id uint32) bool {
	th := win32Call(procOpenThread, threadSuspendResume, 0, uintptr(id))
	if th == 0 {
		return false
	}
	defer closeHandle(syscall.Handle(th)) // the resume result already rules
	prev := win32Call(procResumeThread, th)
	return resumeOK(prev)
}

func (tr *tree) terminate() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.handle == 0 {
		return false
	}
	r := win32Call(procTerminateJobObject, uintptr(tr.handle), forcedExitCode)
	return win32OK(r)
}

func (tr *tree) active() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.handle == 0 {
		return false
	}
	var info jobAccounting
	r := win32Call(procQueryInformationJob,
		uintptr(tr.handle), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0,
	)
	return win32OK(r) && info.ActiveProcesses > 0
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
	//nolint:gosec,noctx // G204: fixed argv over our own pid; noctx: the kill must outlive the expired context
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
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid32(cmd.Process.Pid))
	if err != nil {
		return false
	}
	defer closeHandle(handle) // the liveness read already rules
	var code uint32
	r := win32Call(procGetExitCodeProcess, uintptr(handle), uintptr(unsafe.Pointer(&code)))
	return win32OK(r) && code == stillActive
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

// TrimTail strips the trailing CR-LF pairs a Windows child leaves on a
// captured last line, matching unix's shell-substitution trim.
func TrimTail(s string) string {
	return strings.TrimRight(s, "\r\n")
}

func releaseTree(cmd *osexec.Cmd) {
	v, ok := trees.LoadAndDelete(cmd)
	if !ok {
		return
	}
	tr, ok := v.(*tree)
	if !ok || tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed {
		return
	}
	tr.closed = true
	if tr.handle != 0 {
		closeHandle(tr.handle) // final kill-on-close; no recovery path
		tr.handle = 0
	}
}

func waitStatusOf(exitErr *osexec.ExitError) (waitStatus, bool) {
	if exitErr.ProcessState == nil {
		return waitStatus{}, false
	}
	return waitStatus{status: exitErr.ExitCode()}, true
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
