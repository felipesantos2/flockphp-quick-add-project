package cli

import (
	"errors"
	"io"
	"testing"

	"flock/internal/api"
	"flock/internal/config"
)

func TestReverbStatusSaysWhenTheDaemonIsNotRunning(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOCK_HOME", root)
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), Reverb: true, ReverbPort: 21000})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	cmd := newReverbCommand()
	cmd.SetArgs([]string{"app"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	// It used to exit successfully without printing anything.
	if err := cmd.Execute(); !errors.Is(err, api.ErrDaemonNotRunning) {
		t.Fatalf("err = %v, want %v", err, api.ErrDaemonNotRunning)
	}
}
