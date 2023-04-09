package sys

import (
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

// NOTE: It's copied from https://go-review.googlesource.com/c/sys/+/398434.
func Fsconfig(fd *int, cmd int, key string, value []byte, aux int) (err error) {
	var _p0 *byte
	_p0, err = unix.BytePtrFromString(key)
	if err != nil {
		return
	}
	var _p1 unsafe.Pointer
	if len(value) > 0 {
		_p1 = unsafe.Pointer(&value[0])
	} else {
		_p1 = unsafe.Pointer(&_zero)
	}
	_, _, e1 := unix.Syscall6(unix.SYS_FSCONFIG, uintptr(unsafe.Pointer(fd)), uintptr(cmd), uintptr(unsafe.Pointer(_p0)), uintptr(_p1), uintptr(len(value)), uintptr(aux))
	if e1 != 0 {
		err = e1
	}
	return
}
