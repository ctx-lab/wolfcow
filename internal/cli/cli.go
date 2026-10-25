package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/ctx-lab/wolfcow/pkg/vmm"

	"github.com/spf13/cobra"
)

const Version = "0.0.1"

func Run(args []string, out io.Writer) error {
	cmd := newCommand(vmm.RunShell)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
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
	return root
}
