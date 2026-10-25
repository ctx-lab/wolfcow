package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/ctx-lab/wolfcow/internal/spike"
	"github.com/ctx-lab/wolfcow/internal/vmm"

	"github.com/spf13/cobra"
)

const Version = "0.0.1"

func Run(args []string, out io.Writer) error {
	return RunWithWriters(args, out, out)
}

func RunWithWriters(args []string, out, errOut io.Writer) error {
	cmd := newCommand(vmm.RunShell)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	return cmd.Execute()
}

func newCommand(launch func(string) error) *cobra.Command {
	root := &cobra.Command{
		Use:               "wolfcow",
		Short:             "A minimal libkrun shell launcher",
		Version:           Version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), Version)
			return err
		},
	})
	shell := &cobra.Command{
		Use:   "shell [ROOTFS]",
		Short: "Boot a disposable Linux shell",
		Long:  "Boot a fresh Alpine Linux shell using the bundled macOS runtime.\nAn explicit ROOTFS is copied before boot; guest changes are discarded on exit.\nNever supply a host project or home directory as ROOTFS.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := os.Getenv("WOLFCOW_ROOTFS")
			if len(args) == 1 {
				if cmd.Flags().Changed("rootfs") {
					return fmt.Errorf("supply ROOTFS or --rootfs, not both")
				}
				path = args[0]
			} else if cmd.Flags().Changed("rootfs") {
				path = cmd.Flags().Lookup("rootfs").Value.String()
			}
			return launch(path)
		},
	}
	shell.Flags().String("rootfs", "", "Rootfs source directory (copied; env: WOLFCOW_ROOTFS)")
	root.AddCommand(shell)
	spikeCmd := &cobra.Command{Use: "spike", Short: "Development VM probes", Hidden: true}
	spikeCmd.AddCommand(&cobra.Command{
		Use: "exec -- COMMAND [ARG...]", Short: "Run one bounded command in a disposable guest",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return spike.RunExec(cmd.Context(), args, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	spikeCmd.AddCommand(&cobra.Command{
		Use: "workspace SOURCE -- COMMAND [ARG...]", Short: "Exercise copied workspace and guest file protocol",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return spike.RunWorkspace(cmd.Context(), args[0], args[1:], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	})
	spikeCmd.AddCommand(&cobra.Command{
		Use: "conformance SOURCE", Short: "Check the Stage B guest control contract",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return spike.RunConformance(cmd.Context(), args[0], cmd.OutOrStdout())
		},
	})
	root.AddCommand(spikeCmd)
	return root
}
