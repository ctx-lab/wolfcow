package spike

import (
	"context"
	"fmt"
	"io"

	"github.com/ctx-lab/wolfcow/internal/guest"
	"github.com/ctx-lab/wolfcow/internal/network"
	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/run"
	"github.com/ctx-lab/wolfcow/internal/vmm"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

// RunWorkspace preserves the Stage B development command on the run engine.
func RunWorkspace(ctx context.Context, source string, argv []string, policy network.Policy, stdout, stderr io.Writer) error {
	marker := []byte("written through the guest control protocol\n")
	result, err := (run.Runner{}).Run(ctx, run.Spec{
		Workspace: source, Argv: argv, Network: policy,
		Writes: workspace.Snapshot{"host-note.txt": marker},
	}, stdout, stderr)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "guest kernel: %s\n", result.Kernel); err != nil {
		return err
	}
	for _, change := range result.Changes {
		if _, err := fmt.Fprintf(stdout, "%s %s\n", change.Kind, change.Path); err != nil {
			return err
		}
	}
	if result.Diff != "" {
		if _, err := io.WriteString(stdout, result.Diff); err != nil {
			return err
		}
	}
	if result.Status != "" {
		return fmt.Errorf("guest exec: %s", result.Status)
	}
	if result.Signal != "" {
		return GuestExitError{Code: 1, Signal: result.Signal}
	}
	if result.ExitCode != 0 {
		return GuestExitError{Code: result.ExitCode}
	}
	return nil
}

func fileOperation(ctx context.Context, machine vmm.Machine, request protocol.Message, want string) (protocol.Message, error) {
	return guest.Operation(ctx, machine, request, want)
}
