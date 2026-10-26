// Package guest implements the versioned control operations over a VM stream.
package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
)

type ExecSpec struct {
	Argv      []string
	Dir       string
	Env       []string
	Timeout   time.Duration
	OutputMax int64
}

type ExecResult struct {
	Kernel string
	Code   int
	Signal string
	Status string
}

// Open waits for a valid ready message. The first socket can close while the
// guest's vsock listener is still starting, so only that early EOF is retried.
func Open(ctx context.Context, machine vmm.Machine) (io.ReadWriteCloser, protocol.Message, error) {
	var lastErr error
	for {
		conn, err := machine.OpenControl(ctx)
		if err != nil {
			return nil, protocol.Message{}, err
		}
		if timed, ok := conn.(net.Conn); ok {
			if deadline, has := ctx.Deadline(); has {
				_ = timed.SetReadDeadline(deadline)
			}
		}
		stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
		ready, err := protocol.Read(conn)
		stopCancel()
		if ctx.Err() != nil {
			_ = conn.Close()
			return nil, protocol.Message{}, fmt.Errorf("guest ready: %w", ctx.Err())
		}
		if err == nil {
			if ready.Type != "ready" || ready.Major != protocol.Major || ready.Kernel == "" {
				_ = conn.Close()
				return nil, protocol.Message{}, fmt.Errorf("unsupported guest protocol: type=%q version=%d", ready.Type, ready.Major)
			}
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

func supports(ready protocol.Message, operation string) bool {
	for _, available := range ready.Operations {
		if available == operation {
			return true
		}
	}
	return false
}

func Exec(ctx context.Context, conn io.ReadWriteCloser, ready protocol.Message, spec ExecSpec, stdout, stderr io.Writer) (ExecResult, error) {
	result := ExecResult{Kernel: ready.Kernel}
	if ready.Type != "ready" || ready.Major != protocol.Major || !supports(ready, "exec") {
		return result, fmt.Errorf("guest does not support exec")
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return result, fmt.Errorf("supply a guest command")
	}
	if spec.Dir == "" {
		spec.Dir = "/"
	}
	if spec.Timeout <= 0 || spec.Timeout > time.Duration(protocol.MaxExecMillis)*time.Millisecond {
		return result, fmt.Errorf("invalid exec timeout")
	}
	if spec.OutputMax <= 0 || spec.OutputMax > protocol.MaxOutput {
		return result, fmt.Errorf("invalid output limit")
	}
	execCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	deadline, _ := execCtx.Deadline()
	if timed, ok := conn.(net.Conn); ok {
		_ = timed.SetDeadline(deadline.Add(time.Second))
	}
	stopCancel := context.AfterFunc(execCtx, func() { _ = conn.Close() })
	defer stopCancel()
	request := protocol.Message{Type: "exec.start", ID: "run-exec", Argv: spec.Argv, Dir: spec.Dir, Env: spec.Env, DeadlineMS: deadline.UnixMilli(), OutputMax: spec.OutputMax}
	if err := protocol.Write(conn, request); err != nil {
		return result, fmt.Errorf("send guest exec: %w", err)
	}
	var received int64
	for {
		m, err := protocol.Read(conn)
		if err != nil {
			if execCtx.Err() != nil {
				return result, execCtx.Err()
			}
			return result, fmt.Errorf("guest exec response: %w", err)
		}
		if m.ID != request.ID {
			return result, fmt.Errorf("unexpected guest response id %q", m.ID)
		}
		switch m.Type {
		case "exec.stdout", "exec.stderr":
			received += int64(len(m.Data))
			if received > spec.OutputMax {
				return result, fmt.Errorf("guest output exceeds limit")
			}
			writer := stdout
			if m.Type == "exec.stderr" {
				writer = stderr
			}
			if _, err := writer.Write(m.Data); err != nil {
				return result, err
			}
		case "exec.exit":
			result.Status = m.Status
			result.Signal = m.Signal
			if m.ExitCode != nil {
				result.Code = *m.ExitCode
			} else if m.Status == "" && m.Signal == "" {
				return result, fmt.Errorf("guest exec returned no exit code")
			}
			return result, nil
		case "error":
			return result, fmt.Errorf("guest %s: %s", m.Code, m.Error)
		default:
			return result, fmt.Errorf("unexpected guest response %q", m.Type)
		}
	}
}

// Operation performs one file or workspace request on its own connection.
func Operation(ctx context.Context, machine vmm.Machine, request protocol.Message, want string) (protocol.Message, error) {
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, ready, err := Open(opCtx, machine)
	if err != nil {
		return protocol.Message{}, err
	}
	defer conn.Close()
	operation := map[string]string{"file.read": "read", "file.write": "write", "workspace.diff": "diff"}[request.Type]
	if operation == "" || !supports(ready, operation) {
		return protocol.Message{}, fmt.Errorf("guest does not support %s", operation)
	}
	if timed, ok := conn.(net.Conn); ok {
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
		if opCtx.Err() != nil {
			return protocol.Message{}, opCtx.Err()
		}
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
