package spike

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

// RunWorkspace is the Stage B development probe. It never applies guest changes
// to source; the VM receives copies and returns explicit file data and a diff.
func RunWorkspace(ctx context.Context, source string, argv []string, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("Stage B requires Apple Silicon macOS")
	}
	if len(argv) == 0 {
		return fmt.Errorf("supply a guest command")
	}
	before, err := workspace.ReadSnapshot(source)
	if err != nil {
		return fmt.Errorf("workspace input: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	bundle := filepath.Join(filepath.Dir(exe), "runtime")
	assets, err := vmm.PrepareAgent(bundle)
	if err != nil {
		return err
	}
	defer assets.Cleanup()
	if err := workspace.WriteSnapshot(filepath.Join(assets.Rootfs, "work"), before); err != nil {
		return err
	}
	bootCtx, cancelBoot := context.WithTimeout(ctx, 15*time.Second)
	machine, err := (vmm.Libkrun{Bundle: bundle}).Boot(bootCtx, vmm.BootSpec{Rootfs: assets.Rootfs, Socket: assets.Socket, CPUs: 1, MemoryMiB: 512})
	cancelBoot()
	if err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = machine.Stop(stopCtx)
		_, _ = machine.Wait(stopCtx)
	}()
	// Exercise write and read before execution; both use the guest protocol.
	marker := []byte("written through the guest control protocol\n")
	if _, err := fileOperation(ctx, machine, protocol.Message{Type: "file.write", ID: "write", Path: "host-note.txt", Data: marker}, "file.written"); err != nil {
		return err
	}
	readBack, err := fileOperation(ctx, machine, protocol.Message{Type: "file.read", ID: "read", Path: "host-note.txt"}, "file.data")
	if err != nil {
		return err
	}
	if !bytes.Equal(readBack.Data, marker) {
		return fmt.Errorf("guest file read did not match write")
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, 15*time.Second)
	conn, ready, err := awaitReady(readyCtx, machine)
	cancelReady()
	if err != nil {
		return err
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	execErr := execInDir(ctx, conn, ready, argv, "/work", stdout, stderr)
	stopCancel()
	_ = conn.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result, err := fileOperation(ctx, machine, protocol.Message{Type: "workspace.diff", ID: "diff"}, "workspace.files")
	if err != nil {
		return err
	}
	if len(result.Files) > protocol.MaxFiles {
		return fmt.Errorf("guest file list exceeds limit")
	}
	after := make(workspace.Snapshot, len(result.Files))
	var total int
	for _, name := range result.Files {
		if err := workspace.SafePath(name); err != nil {
			return fmt.Errorf("unsafe guest change: %w", err)
		}
		if _, exists := after[name]; exists {
			return fmt.Errorf("duplicate guest file %q", name)
		}
		m, err := fileOperation(ctx, machine, protocol.Message{Type: "file.read", ID: "pull", Path: name}, "file.data")
		if err != nil {
			return err
		}
		if m.Path != name || len(m.Data) > protocol.MaxFile {
			return fmt.Errorf("invalid guest file response")
		}
		total += len(m.Data)
		if total > protocol.MaxWorkspace {
			return fmt.Errorf("guest workspace exceeds size limit")
		}
		after[name] = m.Data
	}
	verified, err := workspace.Changes(before, after)
	if err != nil {
		return err
	}
	diff, err := workspace.Unified(before, after, verified)
	if err != nil {
		return err
	}
	for _, change := range verified {
		if _, err := fmt.Fprintf(stdout, "%s %s\n", change.Kind, change.Path); err != nil {
			return err
		}
	}
	if diff != "" {
		_, err = io.WriteString(stdout, diff)
	}
	if err != nil {
		return err
	}
	return execErr
}

func fileOperation(ctx context.Context, machine vmm.Machine, request protocol.Message, want string) (protocol.Message, error) {
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, ready, err := awaitReady(opCtx, machine)
	if err != nil {
		return protocol.Message{}, err
	}
	defer conn.Close()
	if ready.Type != "ready" || ready.Major != protocol.Major {
		return protocol.Message{}, fmt.Errorf("unsupported guest protocol")
	}
	operation := map[string]string{"file.read": "read", "file.write": "write", "workspace.diff": "diff"}[request.Type]
	supported := false
	for _, available := range ready.Operations {
		if available == operation {
			supported = true
		}
	}
	if !supported {
		return protocol.Message{}, fmt.Errorf("guest does not support %s", operation)
	}
	if timed, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
		if deadline, has := opCtx.Deadline(); has {
			_ = timed.SetDeadline(deadline)
		}
	}
	stopCancel := context.AfterFunc(opCtx, func() { _ = conn.Close() })
	defer stopCancel()
	if err := protocol.Write(conn, request); err != nil {
		return protocol.Message{}, err
	}
	response, err := protocol.Read(conn)
	if err != nil {
		return protocol.Message{}, err
	}
	if response.ID != request.ID {
		return protocol.Message{}, fmt.Errorf("unexpected guest response id")
	}
	if response.Type == "error" {
		return protocol.Message{}, fmt.Errorf("guest %s: %s", response.Code, response.Error)
	}
	if response.Type != want {
		return protocol.Message{}, fmt.Errorf("unexpected guest response %q", response.Type)
	}
	return response, nil
}
