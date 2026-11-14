package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/paths"
)

// state is the loaded config plus where it lives.
type state struct {
	root string
	cfg  *config.Config
}

func load() (*state, error) {
	root, err := paths.Root()
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}

	return &state{root: root, cfg: cfg}, nil
}

// binaries reads the package manifest of the configured channel (cached in the data directory).
func (s *state) binaries() *binaries.Catalog {
	return binaries.NewCatalog(binaries.DefaultSource(s.root, Version), func() binaries.Channel {
		ch, err := binaries.ParseChannel(s.cfg.Channel)
		if err != nil {
			return binaries.Stable
		}
		return ch
	})
}

func (s *state) client() *api.Client {
	return api.NewClient(paths.Socket(s.root))
}

// commit saves the config and asks a running daemon to apply it.
func (s *state) commit(ctx context.Context, out io.Writer) error {
	if err := s.cfg.Save(s.root); err != nil {
		return err
	}

	status, err := s.client().Reload(ctx)
	if errors.Is(err, api.ErrDaemonNotRunning) {
		fmt.Fprintln(out, "saved — the daemon is not running, changes apply when it starts")
		return nil
	}
	if err != nil {
		return fmt.Errorf("daemon reload: %w", err)
	}

	for _, w := range status.Warnings {
		fmt.Fprintln(out, "warning: "+w)
	}
	return nil
}
