package sys

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// ProcIDMap holds Container ID to Host ID mappings used for User Namespaces
// in Linux. It's alias to syscall.SysProcIDMap.
type ProcIDMap = syscall.SysProcIDMap

type ProcIDMaps []ProcIDMap

// Marshal returns data in the /proc/{uid,gid}_map's format.
func (idMaps ProcIDMaps) Marshal() []byte {
	var data []byte
	for _, im := range idMaps {
		data = append(data,
			[]byte(strconv.Itoa(im.ContainerID)+" "+strconv.Itoa(im.HostID)+" "+strconv.Itoa(im.Size)+"\n")...)
	}
	return data
}

// GetUsernsFD returns a userns file descriptor.
//
// NOTE: It forks a short-live process without CLONE_FILES, which the process
// might hold the copied file descriptors in a short time.
func GetUsernsFD(uidMaps, gidMaps []ProcIDMap) (_ *os.File, retErr error) {
	var pipeFds [2]int

	syscall.ForkLock.Lock()
	if err := syscall.Pipe2(pipeFds[:], syscall.O_CLOEXEC); err != nil {
		syscall.ForkLock.Unlock()
		return nil, fmt.Errorf("failed to open pipe2: %w", err)
	}

	pid, errno := unshareUserns(pipeFds)
	syscall.ForkLock.Unlock()
	if errno != 0 {
		syscall.Close(pipeFds[0])
		syscall.Close(pipeFds[1])
		return nil, fmt.Errorf("failed to unshare userns: %w", errno)
	}

	// close write side
	syscall.Close(pipeFds[1])

	// use non-block read
	pipeR := os.NewFile(uintptr(pipeFds[0]), "userns-piper-"+strconv.Itoa(int(pid)))

	defer func() {
		pipeR.Close()

		_, err := unix.Wait4(int(pid), nil, 0, nil)
		for err == syscall.EINTR {
			_, err = unix.Wait4(int(pid), nil, 0, nil)
		}

		if err != nil {
			logrus.WithError(err).Warnf("failed to find pid(%d) process", pid)
		}
	}()

	f, err := os.Open(fmt.Sprintf("/proc/%d/ns/user", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open userns fd: %w", err)
	}
	defer func() {
		if retErr != nil {
			f.Close()
		}
	}()

	uidCommitFn, uidCloseFn, err := prepareWriteUIDMapsTx(pid, uidMaps)
	if err != nil {
		return nil, err
	}
	defer uidCloseFn()

	gidCommitFn, gidCloseFn, err := prepareWriteGIDMapsTx(pid, gidMaps)
	if err != nil {
		return nil, err
	}
	defer gidCloseFn()

	// STAGE 1: read Ready from child
	if err := waitForSyncFromChild(pipeR, ProcSyncReady); err != nil {
		return nil, err
	}

	if err := uidCommitFn(); err != nil {
		return nil, err
	}

	if err := gidCommitFn(); err != nil {
		return nil, err
	}

	// STAGE 2: wait for bye from child
	if err := waitForSyncFromChild(pipeR, ProcSyncExit); err != nil {
		return nil, err
	}
	return f, nil
}

// unshareUserns unshares process with CLONE_NEWUSER and exits when parent has
// updated the proc/$$/{uid,gid}_map.
//
//go:noinline
//go:norace
func unshareUserns(pipeFds [2]int) (pid uintptr, errno syscall.Errno) {
	var (
		sync, bye   = ProcSyncReady, ProcSyncExit
		current_pid uintptr
		parent_pid  uintptr
	)

	current_pid, _, errno = syscall.RawSyscall(syscall.SYS_GETPID, 0, 0, 0)
	if errno != 0 {
		return
	}

	// block signal during clone
	beforeFork()

	pid, _, errno = syscall.RawSyscall6(syscall.SYS_CLONE,
		uintptr(syscall.SIGCHLD)|syscall.CLONE_NEWUSER,
		0, 0, 0, 0, 0,
	)
	if errno != 0 || pid != 0 {
		// restore all signals
		afterFork()
		return
	}

	// restore all signals
	afterForkInChild()

	// close read side
	_, _, errno = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(pipeFds[0]), 0, 0)
	if errno != 0 {
		goto childerr
	}

	// set pdeathsig with sigkill
	_, _, errno = syscall.RawSyscall6(syscall.SYS_PRCTL,
		syscall.PR_SET_PDEATHSIG,
		uintptr(syscall.SIGKILL),
		0, 0, 0, 0)
	if errno != 0 {
		goto childerr
	}

	// if re-parent happened, exit
	parent_pid, _, errno = syscall.RawSyscall6(syscall.SYS_GETPPID, 0, 0, 0, 0, 0, 0)
	if errno != 0 {
		goto childerr
	}
	if current_pid != parent_pid {
		errno = 255
		goto childerr
	}

	// STAGE 1: sync Ready to parent
	_, _, errno = syscall.RawSyscall6(syscall.SYS_WRITE,
		uintptr(pipeFds[1]), uintptr(unsafe.Pointer(&sync)), unsafe.Sizeof(sync),
		0, 0, 0)
	if errno != 0 {
		goto childerr
	}

	// STAGE 2: say bye to parent
	_, _, errno = syscall.RawSyscall6(syscall.SYS_WRITE,
		uintptr(pipeFds[1]), uintptr(unsafe.Pointer(&bye)), unsafe.Sizeof(bye),
		0, 0, 0)
	if errno != 0 {
		goto childerr
	}

childerr:
	syscall.RawSyscall6(syscall.SYS_EXIT, uintptr(errno), 0, 0, 0, 0, 0)
	panic("unreachable")
}

// ProcSyncType is used for synchronisation between parent and child process
// during setup user namespace mappings.
type ProcSyncType uint8

const (
	// ProcSyncReady is to notify parent that child is running with
	// pdeathsig and parent can start to write uid/gid maps.
	ProcSyncReady ProcSyncType = 1
	// ProcSyncExit is to notify parent that the child is going to exit.
	ProcSyncExit ProcSyncType = 2
)

// String returns human-readable type.
func (typ ProcSyncType) String() string {
	switch typ {
	case ProcSyncReady:
		return "ready"
	case ProcSyncExit:
		return "exit"
	default:
		return "unknown(" + strconv.Itoa(int(typ)) + ")"
	}
}

type (
	commitFunc func() error
	closeFunc  func()
)

func noopCommit() error { return nil }

func noopClose() {}

// prepareWriteUIDMapsTx prepares a transaction to write uid_map. The caller
// should ensure the pid is target one. If the process exits after file open,
// the fd will be become bad one and kernel will refuse to commit data.
func prepareWriteUIDMapsTx(targetPid uintptr, idMaps ProcIDMaps) (commitFunc, closeFunc, error) {
	if len(idMaps) == 0 {
		return noopCommit, noopClose, nil
	}

	procPath := "/proc/" + strconv.Itoa(int(targetPid)) + "/uid_map"

	f, err := os.OpenFile(procPath, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open %s: %w", procPath, err)
	}

	data := idMaps.Marshal()
	return func() error {
			if _, err := f.Write(data); err != nil {
				return fmt.Errorf("failed to write %s into %s: %w",
					string(data), procPath, err)
			}
			return f.Close()
		}, func() {
			f.Close()
		}, nil
}

// prepareWriteGIDMapsTx prepares a transaction to write gid_map. The caller
// should ensure the pid is target one. If the process exits after file open,
// the fd will be become bad one and kernel will refuse to commit data.
func prepareWriteGIDMapsTx(targetPid uintptr, idMaps ProcIDMaps) (commitFunc, closeFunc, error) {
	if len(idMaps) == 0 {
		return noopCommit, noopClose, nil
	}

	procGIDPath := "/proc/" + strconv.Itoa(int(targetPid)) + "/gid_map"
	gidF, err := os.OpenFile(procGIDPath, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open %s: %w", procGIDPath, err)
	}

	procSetgroupPath := "/proc/" + strconv.Itoa(int(targetPid)) + "/setgroups"
	sgF, err := os.OpenFile(procSetgroupPath, os.O_RDWR, 0)
	if err != nil {
		gidF.Close()
		return nil, nil, fmt.Errorf("failed to open %s: %w", procSetgroupPath, err)
	}

	data := idMaps.Marshal()
	return func() error {
			if _, err := sgF.Write([]byte("deny")); err != nil {
				return fmt.Errorf("failed to write deny into %s: %w",
					procSetgroupPath, err)
			}

			if err := sgF.Close(); err != nil {
				return fmt.Errorf("failed to close %s: %w", procSetgroupPath, err)
			}

			if _, err := gidF.Write(data); err != nil {
				return fmt.Errorf("failed to write %s into %s: %w",
					string(data), procGIDPath, err)
			}
			return gidF.Close()
		}, func() {
			sgF.Close()
			gidF.Close()
		}, nil
}

// waitForSyncFromChild reads procSyncType from child.
func waitForSyncFromChild(f *os.File, expected ProcSyncType) error {
	buf := []byte{0}
	_, err := f.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read ProcSyncType: %w", err)
	}

	typ := ProcSyncType(buf[0])
	if typ != expected {
		return fmt.Errorf("expected %s, but got %s", expected, typ)
	}
	return nil
}
