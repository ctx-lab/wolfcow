package cli

import (
	"fmt"
	"io"

	"github.com/ctx-lab/ctx/pkg/vmm"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	config := viper.New()
	config.SetEnvPrefix("CTX")
	config.AutomaticEnv()
	root := &cobra.Command{
		Use:               "ctx",
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
		Long:  "Boot a fresh Alpine Linux shell using the bundled macOS runtime.\nChanges are discarded on exit. An explicit ROOTFS is writable and retained.\nNever supply a host project or home directory as ROOTFS.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GetString("rootfs")
			if len(args) == 1 {
				if cmd.Flags().Changed("rootfs") {
					return fmt.Errorf("supply ROOTFS or --rootfs, not both")
				}
				path = args[0]
			}
			return launch(path)
		},
	}
	shell.Flags().String("rootfs", "", "Disposable rootfs directory (env: CTX_ROOTFS)")
	// The flag exists on this command, so binding cannot fail.
	if err := config.BindPFlag("rootfs", shell.Flags().Lookup("rootfs")); err != nil {
		panic(err)
	}
	root.AddCommand(shell)
	return root
}
