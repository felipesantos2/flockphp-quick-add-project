package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/reverb"
)

func newReverbCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reverb",
		Short: "Run a Laravel app's Reverb server, reachable at wss://<site>.test/app",
		Long: `Flock runs ` + "`php artisan reverb:start`" + ` for the site with its PHP version, on a loopback port, and its
web server sends the site's WebSocket connections (/app/...) and broadcasts (/apps/...) there.
Browsers connect to the site's own address, over HTTPS when the site is secured; the app's .env
needs the REVERB_HOST, REVERB_PORT and REVERB_SCHEME lines ` + "`flock reverb`" + ` shows.

  flock reverb [site]           show the site's Reverb server and its .env lines
  flock reverb on [site]        run it (the project needs ` + "`php artisan install:broadcasting --reverb`" + `)
  flock reverb off [site]       stop it
  flock reverb restart [site]   restart it, e.g. to load changed code`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			return showReverb(cmd, s, site.Name)
		},
	}

	toggle := func(on bool) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if on && !reverb.Installed(site.Path) {
				return errors.New("Laravel Reverb isn't installed in " + site.Path + " — run `php artisan install:broadcasting --reverb` first")
			}
			s.cfg.SetReverb(&site, on)
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			if !on {
				cmd.Printf("stopped %s's Reverb server\n", site.Name)
				return nil
			}
			// commit has already said that a stopped daemon applies the change when it starts.
			if err := showReverb(cmd, s, site.Name); !errors.Is(err, api.ErrDaemonNotRunning) {
				return err
			}
			return nil
		}
	}
	on := &cobra.Command{Use: "on [site]", Short: "Run a site's Reverb server", Args: cobra.MaximumNArgs(1), RunE: toggle(true)}
	off := &cobra.Command{Use: "off [site]", Short: "Stop a site's Reverb server", Args: cobra.MaximumNArgs(1), RunE: toggle(false)}

	restart := &cobra.Command{
		Use:   "restart [site]",
		Short: "Restart a site's Reverb server, e.g. to load changed code",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if !site.Reverb {
				return errors.New("Reverb is off for " + site.Name + " — `flock reverb on` runs it")
			}
			st, err := s.client().RestartReverb(cmd.Context(), site.Name)
			if err != nil {
				return err
			}
			printReverb(cmd, st, site.Name)
			return nil
		},
	}

	cmd.AddCommand(on, off, restart)
	return cmd
}

// showReverb prints the site's Reverb server as the daemon reports it.
func showReverb(cmd *cobra.Command, s *state, name string) error {
	st, err := s.client().Status(cmd.Context())
	if err != nil {
		return err
	}
	printReverb(cmd, st, name)
	return nil
}

func printReverb(cmd *cobra.Command, st api.Status, name string) {
	for _, site := range st.Sites {
		if site.Name != name {
			continue
		}
		r := site.Reverb
		switch {
		case r == nil:
			cmd.Printf("Reverb is off for %s — `flock reverb on` runs it\n", name)
			return
		case r.State == api.ReverbRunning:
			cmd.Printf("%s's Reverb server is running, log: `flock logs %s`\n", name, r.Log)
		default:
			cmd.Printf("%s's Reverb server isn't running: %s\n", name, r.Problem)
		}
		cmd.Printf("in the app's .env:\n  %s\n", strings.Join(r.Env, "\n  "))
	}
}
