// Package spike exercises the Stage A guest control path.
package spike

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

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
	machine, err := (vmm.Libkrun{Bundle: bundle}).Boot(bootCtx, vmm.BootSpec{
		Rootfs: assets.Rootfs, Socket: assets.Socket, CPUs: 1, MemoryMiB: 512,
	})
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
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	return execAfterReady(ctx, conn, ready, argv, stdout, stderr)
}

func awaitReady(ctx context.Context, machine vmm.Machine) (io.ReadWriteCloser, protocol.Message, error) {
	var lastErr error
	for {
		conn, err := machine.OpenControl(ctx)
		if err != nil {
			return nil, protocol.Message{}, err
		}
		if timed, ok := conn.(net.Conn); ok {
			if deadline, ok := ctx.Deadline(); ok {
				_ = timed.SetReadDeadline(deadline)
			}
		}
		ready, err := protocol.Read(conn)
		if err == nil {
			return conn, ready, nil
		}
		_ = conn.Close()
		lastErr = err
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, syscall.ECONNRESET) {
			return nil, protocol.Message{}, fmt.Errorf("guest ready: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, protocol.Message{}, fmt.Errorf("guest ready after connection retries (%v): %w", lastErr, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
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
	if ready.Type != "ready" || ready.Major != protocol.Major || ready.Kernel == "" {
		return fmt.Errorf("unsupported guest protocol: type=%q version=%d", ready.Type, ready.Major)
	}
	canExec := false
	for _, operation := range ready.Operations {
		canExec = canExec || operation == "exec"
	}
	if !canExec {
		return fmt.Errorf("guest does not support exec")
	}
	if _, err := fmt.Fprintf(stderr, "guest kernel: %s\n", ready.Kernel); err != nil {
		return err
	}
	execCtx, cancelExec := context.WithTimeout(ctx, 30*time.Second)
	defer cancelExec()
	deadline, _ := execCtx.Deadline()
	if timed, ok := conn.(net.Conn); ok {
		_ = timed.SetDeadline(deadline.Add(time.Second))
	}
	request := protocol.Message{Type: "exec.start", ID: "spike-exec", Argv: argv, Dir: dir, DeadlineMS: deadline.UnixMilli(), OutputMax: protocol.MaxOutput}
	if err := protocol.Write(conn, request); err != nil {
		return fmt.Errorf("send guest exec: %w", err)
	}
	var received int64
	for {
		m, err := protocol.Read(conn)
		if err != nil {
			if execCtx.Err() != nil {
				_ = protocol.Write(conn, protocol.Message{Type: "exec.cancel", ID: request.ID})
				return execCtx.Err()
			}
			return fmt.Errorf("guest exec response: %w", err)
		}
		if m.ID != request.ID {
			return fmt.Errorf("unexpected guest response id %q", m.ID)
		}
		switch m.Type {
		case "exec.stdout":
			received += int64(len(m.Data))
			if received > protocol.MaxOutput {
				return fmt.Errorf("guest output exceeds limit")
			}
			if _, err := stdout.Write(m.Data); err != nil {
				return err
			}
		case "exec.stderr":
			received += int64(len(m.Data))
			if received > protocol.MaxOutput {
				return fmt.Errorf("guest output exceeds limit")
			}
			if _, err := stderr.Write(m.Data); err != nil {
				return err
			}
		case "exec.exit":
			if m.Status != "" {
				return fmt.Errorf("guest exec: %s", m.Status)
			}
			if m.Signal != "" {
				return GuestExitError{Code: 1, Signal: m.Signal}
			}
			if m.ExitCode == nil {
				return errors.New("guest exec returned no exit code")
			}
			if *m.ExitCode != 0 {
				return GuestExitError{Code: *m.ExitCode}
			}
			return nil
		case "error":
			return fmt.Errorf("guest %s: %s", m.Code, m.Error)
		default:
			return fmt.Errorf("unexpected guest response %q", m.Type)
		}
	}
}
