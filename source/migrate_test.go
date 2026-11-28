package main

import (
	"path/filepath"
	"testing"

	"logdir/internal/core"
)

func TestDefaultLocations(t *testing.T) {
	if core.ConfPath != "/var/lib/logdir/logdir.conf" || filepath.Dir(core.ConfPath) != defaultStateDir {
		t.Fatalf("the config lives in the state directory: %s vs %s", core.ConfPath, defaultStateDir)
	}
	if core.StatusSocket != "/run/logdir/logdir.sock" {
		t.Fatalf("socket: %s", core.StatusSocket)
	}
}
