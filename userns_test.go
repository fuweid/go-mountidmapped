package mountidmapped

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
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

func TestIDMapMount(t *testing.T) {
	dirUid0 := buildTempdir(t,
		fileApplier{name: "foo", content: []byte("bar")},
		fileApplier{name: "fuzz", content: []byte("oops")},
	)
	dirUid1000 := buildTempdir(t) // empty

	f, err := GetUsernsFD([]ProcIDMap{{0, 1000, 1}}, []ProcIDMap{{0, 1000, 1}})
	require.NoError(t, err)
	defer f.Close()

	fdTree, err := IDMapMount(dirUid0, f.Fd())
	require.NoError(t, err)

	err = unix.MoveMount(fdTree, "", -int(unix.EBADF), dirUid1000, unix.MOVE_MOUNT_F_EMPTY_PATH)
	syscall.Close(fdTree)
	require.NoError(t, err)

	t.Cleanup(func() { unmount(t, dirUid1000) })

	{
		fi, err := os.Stat(filepath.Join(dirUid1000, "foo"))
		require.NoError(t, err)
		require.Equal(t, uint32(1000), fi.Sys().(*syscall.Stat_t).Uid)
		require.Equal(t, uint32(1000), fi.Sys().(*syscall.Stat_t).Gid)
	}

	{
		fi, err := os.Stat(filepath.Join(dirUid1000, "fuzz"))
		require.NoError(t, err)
		require.Equal(t, uint32(1000), fi.Sys().(*syscall.Stat_t).Uid)
		require.Equal(t, uint32(1000), fi.Sys().(*syscall.Stat_t).Gid)
	}
}

func buildTempdir(t *testing.T, appliers ...fileApplier) string {
	dir := t.TempDir()

	for _, a := range appliers {
		assert.NoError(t, a.Apply(dir, 0600))
	}
	return dir
}

type fileApplier struct {
	name    string
	content []byte
}

func (a fileApplier) Apply(rootDir string, perm os.FileMode) error {
	return os.WriteFile(
		filepath.Join(rootDir, a.name),
		a.content,
		perm,
	)

}

func unmount(t *testing.T, dirPath string) {
	for {
		if err := unix.Unmount(dirPath, 0); err != nil {
			if err == unix.EINVAL {
				return
			}

			t.Fatalf("failed to unmount %s: %v", dirPath, err)
			return
		}
	}
}

func currentUserns(t *testing.T) uint64 {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d/ns/user", os.Getpid()))
	assert.NoError(t, err)

	return fi.Sys().(*syscall.Stat_t).Ino
}
