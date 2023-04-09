package sys

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var initUsernsFD uint64

func init() {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d/ns/user", os.Getpid()))
	if err != nil {
		panic("failed to stat current userns")
	}
	initUsernsFD = fi.Sys().(*syscall.Stat_t).Ino
}

func TestGetUsernsFD(t *testing.T) {
	t.Logf("current userns id: %v", initUsernsFD)

	f, err := GetUsernsFD([]ProcIDMap{{0, 1000, 1}}, []ProcIDMap{{0, 1000, 1}})
	require.NoError(t, err)
	defer f.Close()

	fi, err := f.Stat()
	require.NoError(t, err)
	newUsernsID := fi.Sys().(*syscall.Stat_t).Ino

	t.Logf("new userns id: %v", newUsernsID)

	require.Equal(t, true, newUsernsID != initUsernsFD)

	checkCurrentUsernsID := currentUserns(t)
	t.Logf("checking current userns id: %v", checkCurrentUsernsID)
	require.Equal(t, true, initUsernsFD == checkCurrentUsernsID)
}

func TestGetUsernsFDConcurrent(t *testing.T) {
	t.Logf("current userns id: %v", initUsernsFD)

	var wg sync.WaitGroup

	n := 100
	ch := make(chan *os.File, n)
	for i := 0; i < n; i++ {
		wg.Add(1)

		i := i + 1000
		go func() {
			defer wg.Done()

			f, err := GetUsernsFD(
				[]ProcIDMap{{0, i, 1}},
				[]ProcIDMap{{0, i, 1}},
			)
			require.NoError(t, err)
			ch <- f
		}()
	}
	wg.Wait()

	noDupUserns := map[uint64]struct{}{}
	for i := 0; i < n; i++ {
		f := <-ch

		fi, err := f.Stat()
		require.NoError(t, err)

		usernsID := fi.Sys().(*syscall.Stat_t).Ino
		f.Close()

		_, ok := noDupUserns[usernsID]
		require.Equal(t, false, ok, "should not have duplicate userns ID")
		require.NotEqual(t, initUsernsFD, usernsID)
		noDupUserns[usernsID] = struct{}{}
	}
}

func currentUserns(t *testing.T) uint64 {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d/ns/user", os.Getpid()))
	assert.NoError(t, err)

	return fi.Sys().(*syscall.Stat_t).Ino
}
