package mountidmapped

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	FSCONFIG_SET_FLAG        = 0x0
	FSCONFIG_SET_STRING      = 0x1
	FSCONFIG_SET_BINARY      = 0x2
	FSCONFIG_SET_PATH        = 0x3
	FSCONFIG_SET_PATH_EMPTY  = 0x4
	FSCONFIG_SET_FD          = 0x5
	FSCONFIG_CMD_CREATE      = 0x6
	FSCONFIG_CMD_RECONFIGURE = 0x7
)

// Single-word zero for use when we need a valid pointer to 0 bytes.
var _zero uintptr

// Fsconfig is to call SYS_FSCONFIG syscall.
//
// NOTE: It's based on https://go-review.googlesource.com/c/sys/+/398434.
func Fsconfig(fd int, cmd int, key string, value string, aux int) (err error) {
	var _p0 unsafe.Pointer
	if len(key) > 0 {
		var _v0 *byte
		_v0, err = unix.BytePtrFromString(key)
		if err != nil {
			return
		}
		_p0 = unsafe.Pointer(_v0)
	} else {
		_p0 = nil
	}

	var _p1 unsafe.Pointer
	if len(value) > 0 {
		var _v0 *byte
		_v0, err = unix.BytePtrFromString(value)
		if err != nil {
			return err
		}
		_p1 = unsafe.Pointer(_v0)
	} else if cmd == FSCONFIG_CMD_CREATE {
		_p1 = nil
	} else {
		_p1 = unsafe.Pointer(&_zero)
	}

	_, _, e1 := unix.Syscall6(unix.SYS_FSCONFIG, uintptr(fd), uintptr(cmd), uintptr(_p0), uintptr(_p1), uintptr(aux), 0)
	if e1 != 0 {
		err = e1
	}
	return
}

// IDMapMount calls mount_setattr syscall with a given userns fd.
func IDMapMount(dir string, usernsFD uintptr) (int, error) {
	dirFD, err := openTree(dir, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return 0, fmt.Errorf("failed to sys_open_tree to %s: %w", dir, err)
	}

	attr := &unix.MountAttr{
		Attr_set:  unix.MOUNT_ATTR_IDMAP,
		Userns_fd: uint64(usernsFD),
	}
	if err := unix.MountSetattr(dirFD, "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE, attr); err != nil {
		unix.Close(dirFD)
		return 0, fmt.Errorf("failed to do idmap mount for %s: %w", dir, err)
	}
	return dirFD, nil
}

func openTree(path string, flags int) (fd int, err error) {
	var _p0 *byte

	if _p0, err = syscall.BytePtrFromString(path); err != nil {
		return 0, err
	}

	r, _, e1 := unix.Syscall6(uintptr(unix.SYS_OPEN_TREE),
		uintptr(0), uintptr(unsafe.Pointer(_p0)), uintptr(flags),
		0, 0, 0)
	if e1 != 0 {
		err = e1
	}
	return int(r), nil
}
