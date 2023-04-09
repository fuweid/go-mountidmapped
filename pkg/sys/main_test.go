package sys

import (
	"fmt"
	"os"
	"testing"
)

func requireRoot() {
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "This test must be run as root.")
		os.Exit(1)
	}
}

func TestMain(m *testing.M) {
	requireRoot()

	os.Exit(m.Run())
}
