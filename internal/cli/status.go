package cli

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/project"
	"flock/internal/share"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the state of the Flock daemon and its services",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			st, err := s.client().Status(cmd.Context())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "daemon    running (pid %d, %s, up %s)\n", st.PID, st.Version, time.Since(st.StartedAt).Round(time.Second))
			switch {
			case st.DNS.Wildcard:
				fmt.Fprintf(out, "dns       *.%s → 127.0.0.1, any subdomain (server on %s)\n", st.DNS.TLD, st.DNS.Addr)
			case st.DNS.TLD != "" && runtime.GOOS == "windows":
				fmt.Fprintf(out, "dns       site domains and extra domains → 127.0.0.1 (hosts file); other *.%s subdomains don't resolve: %s\n",
					st.DNS.TLD, st.DNS.WildcardProblem)
			case st.DNS.TLD != "":
				fmt.Fprintf(out, "dns       *.%s doesn't resolve to Flock: %s\n", st.DNS.TLD, st.DNS.WildcardProblem)
			}
			fmt.Fprintf(out, "web       http :%d, https :%d\n", st.HTTPPort, st.HTTPSPort)
			fmt.Fprintf(out, "mail      smtp://%s\n", st.Mail)
			if st.Postgres != "" {
				fmt.Fprintf(out, "postgres  %s\n", st.Postgres)
			}
			for _, x := range st.Extras {
				state := fmt.Sprintf("%s on 127.0.0.1:%d", x.Version, x.Port)
				if !x.Enabled {
					state += " (stopped)"
				}
				fmt.Fprintf(out, "%-9s %s\n", x.ID, state)
			}
			if st.Xdebug {
				line := "on for requests that ask for it (port 9003)"
				var debugging []string
				for _, site := range st.Sites {
					if site.Debugging {
						debugging = append(debugging, site.Domain)
					}
				}
				if len(debugging) > 0 {
					line += ", PhpStorm breakpoints in " + strings.Join(debugging, ", ")
				}
				fmt.Fprintln(out, "xdebug    "+line)
			}
			for _, site := range st.Sites {
				if site.Share != nil {
					fmt.Fprintf(out, "shared    %s\n", shareLine(*site.Share))
				}
			}
			for _, p := range st.Processes {
				state := "up"
				if !p.Up {
					state = fmt.Sprintf("DOWN (restarted %d×, last error: %s)", p.Restarts, p.LastErr)
				} else if p.Restarts > 0 {
					state = fmt.Sprintf("up (restarted %d×)", p.Restarts)
				}
				fmt.Fprintf(out, "process   %s %s\n", p.Name, state)
			}
			if d := st.Defaults; d != nil {
				for _, in := range []struct {
					name string
					api.DefaultInstall
				}{{"PHP", d.PHP}, {"Node.js", d.Node}} {
					if line := defaultInstallLine(in.name, in.DefaultInstall); line != "" {
						fmt.Fprintf(out, "setup     %s\n", line)
					}
				}
			}
			for _, w := range st.Warnings {
				fmt.Fprintf(out, "warning   %s\n", w)
			}

			return nil
		},
	}
}

func newSitesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sites",
		Short: "List linked and parked sites, and proxies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SITE\tPHP\tURL\tROOT\tPATH")

			st, err := s.client().Status(cmd.Context())
			if err == nil {
				for _, site := range st.Sites {
					url := site.URL
					if site.Problem != "" {
						url = "⚠ " + site.Problem
					}
					version, root := site.PHP, rootColumn(docRootOf(site))
					if site.Server == "frankenphp" {
						version += " FrankenPHP"
						if site.Worker == "octane" {
							version += " (Octane)"
						}
					}
					if site.Reverb != nil {
						version += " + Reverb"
					}
					if site.Type == config.TypeNode {
						version, root = "Node.js "+site.Node, "-"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", site.Name, version, url, root, site.Path)
				}
				printProxyRows(w, proxyRows(&st, s.cfg))
				return w.Flush()
			}

			// Daemon down: show what the config says.
			for _, site := range s.cfg.AllSites() {
				scheme := "http"
				if site.Secured {
					scheme = "https"
				}
				version, root := s.cfg.PHPFor(site), rootColumn(project.RootFor(&site))
				if site.Kind() == config.TypeNode {
					version, root = "Node.js "+s.cfg.NodeFor(site), "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s://%s\t%s\t%s\n", site.Name, version, scheme, s.cfg.Domain(site), root, site.Path)
			}
			printProxyRows(w, proxyRows(nil, s.cfg))
			return w.Flush()
		},
	}
}

// printProxyRows adds the proxies to `flock sites`: "proxy" where a site has its PHP version, and the
// upstream where it has its folder.
func printProxyRows(w io.Writer, rows []proxyRow) {
	for _, p := range rows {
		fmt.Fprintf(w, "%s\tproxy\t%s\t-\t%s\n", p.name, p.url, p.upstream)
	}
}

// shareLine describes a site's share for `flock status`, so a running tunnel is never out of sight.
func shareLine(st api.ShareStatus) string {
	switch st.State {
	case share.Building:
		return st.Site + ": building the assets"
	case share.Starting:
		return st.Site + ": starting the tunnel"
	case share.Failed:
		return st.Site + ": failed: " + st.Error
	}
	var details []string
	if st.ExpiresAt != nil {
		details = append(details, "ends "+st.ExpiresAt.Local().Format(time.Kitchen))
	}
	if len(st.Allow) > 0 {
		details = append(details, "only "+strings.Join(st.Allow, ", "))
	}
	line := st.Site + " → " + st.URL
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	return line
}

// defaultInstallLine describes the first run's install of a default version ("" once it is set).
func defaultInstallLine(name string, in api.DefaultInstall) string {
	version := name
	if in.Version != "" {
		version += " " + in.Version
	}
	switch in.State {
	case api.DefaultInstalling:
		return "installing " + version + " as the default"
	case api.DefaultFailed:
		return fmt.Sprintf("installing %s failed: %s", version, in.Error)
	case api.DefaultWaiting:
		return fmt.Sprintf("%s is installed as the default once Flock can download it", version)
	}
	return ""
}
