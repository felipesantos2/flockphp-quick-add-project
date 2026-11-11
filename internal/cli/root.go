// Package cli implements the `flock` command.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time: go build -ldflags "-X flock/internal/cli.Version=1.2.3".
var Version = "dev"

// NewRootCommand builds the `flock` command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "flock",
		Short:         "Flock — a native local Laravel development environment",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	// cmd.Print* defaults to stderr in cobra; regular output belongs on stdout.
	root.SetOut(os.Stdout)

	root.AddCommand(
		newInstallCommand(), newConflictsCommand(), newUninstallCommand(), newPrivilegedSetupCommand(),
		newDaemonCommand(), newRestartCommand(), newStatusCommand(), newLogsCommand(), newVersionCommand(), newUpdateCommand(),
		newAppCommand(), newSitesCommand(), newLinkCommand(), newUnlinkCommand(), newParkCommand(), newUnparkCommand(), newProxyCommand(), newUnproxyCommand(), newProxiesCommand(),
		newSecureCommand(true), newSecureCommand(false), newDomainCommand(), newWorktreeCommand(), newShareCommand(), newUseCommand(),
		newIsolateCommand(), newUnisolateCommand(), newIsolatedCommand(), newPinCommand(), newUnpinCommand(), newRootCommand(),
		newPHPCommand(), newNodeCommand(), newServicesCommand(), newXdebugCommand(), newDebugCommand(), newFrankenPHPCommand(), newDumpsCommand(), newEventsCommand(), newMailCommand(),
		newExecCommand(), newMCPCommand(), newBoostCommand(), newChannelCommand(), newExtCommand(),
	)

	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Flock version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println("flock " + Version)
		},
	}
}
