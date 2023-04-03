package sys

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUsernsFD(t *testing.T) {
	currentUsernsID := currentUserns(t)
	t.Logf("current userns id: %v", currentUsernsID)

	f, err := GetUsernsFD()
	assert.NoError(t, err)
	defer f.Close()

	fi, err := f.Stat()
	assert.NoError(t, err)
	newUsernsID := fi.Sys().(*syscall.Stat_t).Ino

	t.Logf("new userns id: %v", newUsernsID)

	assert.Equal(t, true, newUsernsID != currentUsernsID)

	checkCurrentUsernsID := currentUserns(t)
	t.Logf("checking current userns id: %v", checkCurrentUsernsID)
	assert.Equal(t, true, currentUsernsID == checkCurrentUsernsID)
}

func currentUserns(t *testing.T) uint64 {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d/ns/user", os.Getpid()))
	assert.NoError(t, err)

	return fi.Sys().(*syscall.Stat_t).Ino
}
