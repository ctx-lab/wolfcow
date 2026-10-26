// Package spike retains development probes for the guest control path.
package spike

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ctx-lab/wolfcow/internal/guest"
	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
)

type GuestExitError struct {
	Code   int
	Signal string
}

func (e GuestExitError) Error() string {
	if e.Signal != "" {
		return fmt.Sprintf("guest terminated by %s", e.Signal)
	}
	return fmt.Sprintf("guest exited with status %d", e.Code)
}
func (e GuestExitError) ExitCode() int { return e.Code }

func RunExec(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("Stage A requires Apple Silicon macOS")
	}
	if len(argv) == 0 {
		return fmt.Errorf("supply a guest command")
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
	readyCtx, cancelReady := context.WithTimeout(ctx, 15*time.Second)
	conn, ready, err := awaitReady(readyCtx, machine)
	cancelReady()
	if err != nil {
		return err
	}
	defer conn.Close()
	return execAfterReady(ctx, conn, ready, argv, stdout, stderr)
}

func awaitReady(ctx context.Context, machine vmm.Machine) (io.ReadWriteCloser, protocol.Message, error) {
	return guest.Open(ctx, machine)
}

func runControl(ctx context.Context, conn io.ReadWriteCloser, argv []string, stdout, stderr io.Writer) error {
	if timed, ok := conn.(net.Conn); ok {
		_ = timed.SetReadDeadline(time.Now().Add(15 * time.Second))
	}
	ready, err := protocol.Read(conn)
	if err != nil {
		return fmt.Errorf("guest ready: %w", err)
	}
	return execAfterReady(ctx, conn, ready, argv, stdout, stderr)
}

func execAfterReady(ctx context.Context, conn io.ReadWriteCloser, ready protocol.Message, argv []string, stdout, stderr io.Writer) error {
	return execInDir(ctx, conn, ready, argv, "/", stdout, stderr)
}

func execInDir(ctx context.Context, conn io.ReadWriteCloser, ready protocol.Message, argv []string, dir string, stdout, stderr io.Writer) error {
	if _, err := fmt.Fprintf(stderr, "guest kernel: %s\n", ready.Kernel); err != nil {
		return err
	}
	result, err := guest.Exec(ctx, conn, ready, guest.ExecSpec{Argv: argv, Dir: dir, Timeout: 30 * time.Second, OutputMax: protocol.MaxOutput}, stdout, stderr)
	if err != nil {
		return err
	}
	if result.Status != "" {
		return fmt.Errorf("guest exec: %s", result.Status)
	}
	if result.Signal != "" {
		return GuestExitError{Code: 1, Signal: result.Signal}
	}
	if result.Code != 0 {
		return GuestExitError{Code: result.Code}
	}
	return nil
}
