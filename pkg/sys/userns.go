package sys

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// GetUsernsFD returns a userns file descriptor.
//
// NOTE: It forks a short-live process without CLONE_FILES, which the process
// might hold the copied file descriptors in a short time.
func GetUsernsFD() (*os.File, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var pipeFds [2]int
	if err := syscall.Pipe2(pipeFds[:], syscall.O_CLOEXEC); err != nil {
		return nil, fmt.Errorf("failed to open pipe2: %w", err)
	}

	pid, errno := unshareUserns(pipeFds)
	if errno != 0 {
		syscall.Close(pipeFds[0])
		syscall.Close(pipeFds[1])
		return nil, fmt.Errorf("failed to unshare userns: %w", errno)
	}

	defer func() {
		_, err := unix.Wait4(int(pid), nil, 0, nil)
		for err == syscall.EINTR {
			_, err = unix.Wait4(int(pid), nil, 0, nil)
		}

		if err != nil {
			logrus.WithError(err).Warnf("failed to find pid=%d process", pid)
		}
	}()

	syscall.Close(pipeFds[0])

	f, err := os.Open(fmt.Sprintf("/proc/%d/ns/user", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open userns fd: %w", err)
	}

	// NOTE: Ensure that pid isn't recycled.
	ready := []byte{1}
	_, err = syscall.Write(pipeFds[1], ready)
	syscall.Close(pipeFds[1])
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to read ready notify: %w", err)
	}
	return f, nil
}

func unshareUserns(pipeFds [2]int) (pid uintptr, errno syscall.Errno) {
	var ready uintptr

	// block signal during clone
	beforeFork()

	pid, _, errno = syscall.RawSyscall(syscall.SYS_CLONE,
		uintptr(syscall.SIGCHLD)|syscall.CLONE_NEWUSER,
		0, 0,
	)
	if errno != 0 || pid != 0 {
		// restore all signals
		afterFork()
		return
	}

	// restore all signals
	afterForkInChild()

	_, _, errno = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(pipeFds[1]), 0, 0)
	if errno != 0 {
		goto childerr
	}

	_, _, errno = syscall.RawSyscall(syscall.SYS_READ,
		uintptr(pipeFds[0]), uintptr(unsafe.Pointer(&ready)), unsafe.Sizeof(ready))
	if errno != 0 {
		goto childerr
	}

childerr:
	syscall.RawSyscall(syscall.SYS_EXIT, uintptr(errno), 0, 0)
	panic("unreachable")
}
