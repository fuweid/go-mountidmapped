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

	if err := syscall.Close(pipeFds[1]); err != nil {
		syscall.Close(pipeFds[0])
		return nil, fmt.Errorf("failed to close pipe2(writer): %w", err)
	}

	f, err := os.Open(fmt.Sprintf("/proc/%d/ns/user", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open userns fd: %w", err)
	}

	buf := make([]byte, 1)
	_, err = syscall.Read(pipeFds[0], buf)
	syscall.Close(pipeFds[0])

	if err != nil {
		return nil, fmt.Errorf("failed to read ready notify: %w", err)
	}
	return f, nil
}

func unshareUserns(pipeFds [2]int) (pid uintptr, errno syscall.Errno) {
	var ready byte = 1

	// block signal during clone
	beforeFork()

	pid, _, errno = syscall.RawSyscall6(syscall.SYS_CLONE,
		uintptr(syscall.SIGCHLD)|syscall.CLONE_NEWUSER|syscall.CLONE_FILES,
		0,
		0,
		0,
		0,
		0,
	)
	if errno != 0 || pid != 0 {
		// restore all signals
		afterFork()
		return
	}

	// restore all signals
	afterForkInChild()

	// TODO(fuweid): limit the buffer
	_, _, errno = syscall.RawSyscall(syscall.SYS_WRITE,
		uintptr(pipeFds[1]), uintptr(unsafe.Pointer(&ready)), unsafe.Sizeof(ready))
	if errno != 0 {
		goto childerr
	}

childerr:
	syscall.RawSyscall(syscall.SYS_EXIT, uintptr(errno), 0, 0)
	panic("unreachable")
}
