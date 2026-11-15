package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Calls meant for util-linux's flock reach it; Flock's own commands, help and `flk` stay with Flock.
func TestPassThrough(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "flock")
	if err := os.WriteFile(fake, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	old := utilLinuxFlock
	utilLinuxFlock = []string{fake}
	t.Cleanup(func() { utilLinuxFlock = old })

	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"/home/a/.local/share/flock/bin/flock", "-n", "/tmp/lock", "make"}, true},
		{[]string{"flock", "9"}, true},
		{[]string{"flock", "/var/lock/backup", "-c", "rsync"}, true},
		{[]string{"flock", "--version"}, true},
		{[]string{"flock"}, false},
		{[]string{"flock", "link"}, false},
		{[]string{"flock", "app", "create", "demo"}, false},
		{[]string{"flock", "status", "--json"}, false},
		{[]string{"flock", "--help"}, false},
		{[]string{"flock", "__complete", "li"}, false},
		{[]string{"flk", "-n", "/tmp/lock"}, false},
	} {
		path, ok := PassThrough(c.args)
		if ok != c.want || ok && path != fake {
			t.Errorf("%v: %q, %v; want %v", c.args, path, ok, c.want)
		}
	}

	utilLinuxFlock = []string{filepath.Join(t.TempDir(), "missing")}
	if _, ok := PassThrough([]string{"flock", "-n", "/tmp/lock"}); ok {
		t.Error("passed through to a missing util-linux flock")
	}
}
