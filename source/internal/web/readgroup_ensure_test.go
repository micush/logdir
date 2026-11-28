package web

import (
	"os"
	"strings"
	"testing"

	"logdir/internal/core"
)

func TestEnsureReadGroupMakesOnlyTheDefaultGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root creates the group")
	}
	old := core.UsersRun
	t.Cleanup(func() { core.UsersRun = old })
	var calls []string
	core.UsersRun = func(stdin, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	EnsureReadGroup(core.WebConfig{ReadGroup: "logdir-ro"})
	if len(calls) != 1 || calls[0] != "groupadd --system logdir-ro" {
		t.Fatalf("default group: %v", calls)
	}
	calls = nil
	EnsureReadGroup(core.WebConfig{ReadGroup: "EX\\viewers"}) // a directory group is never made here
	EnsureReadGroup(core.WebConfig{ReadGroup: ""})
	if len(calls) != 0 {
		t.Fatalf("made a group it should not: %v", calls)
	}
}
