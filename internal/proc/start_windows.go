//go:build windows

package proc

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start starts command in Inkuna's job, which Windows ends, with everything
// in it, once Inkuna ends, however it ends: Windows offers no cheap way to
// read another process's environment, so the next Inkuna could not find
// what a crash left running by its mark (EndLeftOver). What command starts
// joins the job with it. It starts suspended and runs once it is in the
// job, so it cannot start a child that escapes first. One that cannot join
// still runs, and the reason is logged.
func Start(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := command.Start(); err != nil {
		return err
	}
	pid := uint32(command.Process.Pid)
	if err := join(pid); err != nil {
		log.Printf("inkuna: %s will not end with Inkuna: %v", command.Path, err)
	}
	if err := resume(pid); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("start %s: resume it: %w", command.Path, err)
	}
	return nil
}

var (
	jobOnce sync.Once
	job     windows.Handle
	jobErr  error
)

// inkunaJob is the job every process Start starts joins. Its handle is
// never closed: Windows closes it when Inkuna ends, and the job's limit
// then ends what is still in it.
func inkunaJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		handle, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("create the job: %w", err)
			return
		}
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
		}
		if _, err := windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
			_ = windows.CloseHandle(handle)
			jobErr = fmt.Errorf("make the job end its processes with Inkuna: %w", err)
			return
		}
		job = handle
	})
	return job, jobErr
}

func join(pid uint32) error {
	job, err := inkunaJob()
	if err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open the process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("add it to the job: %w", err)
	}
	return nil
}

// resume resumes the threads of a process started suspended, which has
// only its first. Go does not keep that thread's handle, so it is found by
// the process's ID.
func resume(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("list threads: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return fmt.Errorf("open its thread: %w", openErr)
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("resume its thread: %w", resumeErr)
		}
		resumed = true
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("list threads: %w", err)
	}
	if !resumed {
		return errors.New("it has no thread")
	}
	return nil
}
