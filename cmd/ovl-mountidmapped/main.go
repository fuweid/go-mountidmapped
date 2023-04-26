package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fuweid/go-mountidmapped"

	"github.com/containerd/containerd/mount"
	"golang.org/x/sys/unix"
)

var (
	lowerDirs string
	upperDir  string
	workDir   string
	mergedDir string
	idmapping string
)

func init() {
	flag.StringVar(&lowerDirs, "lowerDirs", "", "[Required] The lowerdir(<dir>[:<dir>]) of overlayfs")
	flag.StringVar(&mergedDir, "mergedDir", "", "[Required] The merged point of overlayfs")
	flag.StringVar(&upperDir, "upperDir", "", "The upperdir of overlayfs")
	flag.StringVar(&workDir, "workDir", "", "The workdir of overlayfs")
	flag.StringVar(&idmapping, "idmapping", "", "[Required] The idmapping(<id-from>:<id-to>:<id-range>) of both uid and gid")
}

func main() {
	flag.Parse()

	if err := validateFlags(); err != nil {
		log.Fatalf("failed to validate: %v", err)
	}

	idMap, _ := parseIDMapping(idmapping)

	// create userns
	usernsFD, err := mountidmapped.GetUsernsFD(
		[]mountidmapped.ProcIDMap{idMap},
		[]mountidmapped.ProcIDMap{idMap},
	)
	if err != nil {
		log.Fatalf("failed to create new userns: %v", err)
	}
	defer usernsFD.Close()

	// idMap mount lowerdir into temp
	tempDir, err := os.MkdirTemp("", "ovl-mountidmapped-XXXX")
	if err != nil {
		log.Fatalf("failed to create tempDir: %v", err)
	}

	newLowerDirs, doneFn, err := idmapMountLowerdirs(tempDir, lowerDirs, usernsFD)
	if err != nil {
		log.Fatalf("failed to idmap mount lowerdir: %v", err)
	}

	// make overlay mount
	err = makeOvlFsMount(upperDir, workDir, strings.Join(newLowerDirs, ":"), mergedDir)

	// cleanup
	doneFn()
	usernsFD.Close()
	os.RemoveAll(tempDir)

	if err != nil {
		log.Fatalf("failed to make overlay mount on %s: %v", mergedDir, err)
	}
}

func makeOvlFsMount(upperDir, workDir, lowerDirs, mergedDir string) error {
	return (&mount.Mount{
		Type:   "overlay",
		Source: "overlay",
		Options: []string{
			"workdir=" + workDir,
			"upperdir=" + upperDir,
			"lowerdir=" + lowerDirs,
		},
	}).Mount(mergedDir)
}

func idmapMountLowerdirs(tempDir string, lowerDirs string, usernsFD *os.File) (_ []string, cleanup func(), retErr error) {
	res := make([]string, 0)

	cleanup = func() {
		for _, lowerDir := range res {
			unix.Unmount(lowerDir, 0)
		}
	}

	defer func() {
		if retErr != nil {
			cleanup()
		}
	}()

	for idx, lowerDir := range strings.Split(lowerDirs, ":") {
		newLowerDir := filepath.Join(tempDir, strconv.Itoa(idx))

		err := func() error {
			if err := os.MkdirAll(newLowerDir, 0600); err != nil {
				return fmt.Errorf("failed to create new lowerDir %s: %w", newLowerDir, err)
			}

			fdTree, err := mountidmapped.IDMapMount(lowerDir, usernsFD.Fd())
			if err != nil {
				return fmt.Errorf("failed to idmap mount %s: %v", lowerDir, err)
			}
			defer unix.Close(fdTree)

			err = unix.MoveMount(fdTree, "", -int(unix.EBADF), newLowerDir, unix.MOVE_MOUNT_F_EMPTY_PATH)
			if err != nil {
				return fmt.Errorf("failed to move_mount to %s: %v", newLowerDir, err)
			}
			return nil
		}()
		if err != nil {
			return nil, nil, err
		}

		res = append(res, newLowerDir)
	}
	return res, cleanup, nil
}

func validateFlags() error {
	if len(lowerDirs) == 0 {
		return fmt.Errorf("flag lowerDir is required")
	}

	if len(mergedDir) == 0 {
		return fmt.Errorf("flag mergedDir is required")
	}

	hasUpperdir := len(upperDir) != 0
	hasWorkdir := len(workDir) != 0

	if hasWorkdir != hasUpperdir {
		return fmt.Errorf("upperDir should be set with workDir")
	}

	_, err := parseIDMapping(idmapping)
	return err
}

var emptyIDMap mountidmapped.ProcIDMap

func parseIDMapping(mapping string) (mountidmapped.ProcIDMap, error) {
	parts := strings.SplitN(mapping, ":", 3)
	if len(parts) != 3 {
		return emptyIDMap, fmt.Errorf("expect <id-from>:<id-to>:<id-range>, but got %s", mapping)
	}

	idFrom, err := strconv.ParseInt(parts[0], 10, 0)
	if err != nil {
		return emptyIDMap, fmt.Errorf("failed to parse <id-from>(%s): %w", parts[0], err)
	}

	idTo, err := strconv.ParseInt(parts[1], 10, 0)
	if err != nil {
		return emptyIDMap, fmt.Errorf("failed to parse <id-to>(%s): %w", parts[1], err)
	}

	idRange, err := strconv.ParseInt(parts[2], 10, 0)
	if err != nil {
		return emptyIDMap, fmt.Errorf("failed to parse <id-range>(%s): %w", parts[2], err)
	}

	return mountidmapped.ProcIDMap{
		ContainerID: int(idFrom),
		HostID:      int(idTo),
		Size:        int(idRange),
	}, nil
}
