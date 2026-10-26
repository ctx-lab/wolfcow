package vmm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// BootSpec contains only prepared guest-owned paths. The source project never
// crosses this interface.
type BootSpec struct {
	Rootfs    string
	Socket    string
	NetSocket string // Optional gvproxy transport; empty keeps the guest offline.
	CPUs      uint8
	MemoryMiB uint32
}

type Exit struct {
	Code   int
	Signal string
}

type Backend interface {
	Boot(context.Context, BootSpec) (Machine, error)
}

type Machine interface {
	OpenControl(context.Context) (io.ReadWriteCloser, error)
	Wait(context.Context) (Exit, error)
	Stop(context.Context) error
}

type Libkrun struct{ Bundle string }

func (b Libkrun) Boot(ctx context.Context, spec BootSpec) (Machine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(spec.Rootfs) || !filepath.IsAbs(spec.Socket) {
		return nil, fmt.Errorf("VM paths must be absolute")
	}
	if spec.NetSocket != "" {
		if !filepath.IsAbs(spec.NetSocket) {
			return nil, fmt.Errorf("network socket path must be absolute")
		}
		info, err := os.Lstat(spec.NetSocket)
		if err != nil {
			return nil, fmt.Errorf("network socket: %w", err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("network endpoint is not a socket")
		}
	}
	if spec.CPUs < 1 || spec.CPUs > 8 || spec.MemoryMiB < 128 || spec.MemoryMiB > 8192 {
		return nil, fmt.Errorf("VM resources out of Stage A range")
	}
	if _, err := os.Lstat(spec.Socket); err == nil {
		return nil, fmt.Errorf("control socket already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	helper := filepath.Join(b.Bundle, "wolfcow-krun")
	args := []string{"--agent", spec.Rootfs, spec.Socket, fmt.Sprint(spec.CPUs), fmt.Sprint(spec.MemoryMiB)}
	if spec.NetSocket != "" {
		args = []string{"--agent-net", spec.Rootfs, spec.Socket, spec.NetSocket, fmt.Sprint(spec.CPUs), fmt.Sprint(spec.MemoryMiB)}
	}
	cmd := exec.Command(helper, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	// A writer wrapper makes exec.Cmd give the helper pipes instead of the user's
	// TTY. libkrun otherwise puts that TTY in raw mode while the VM runs.
	cmd.Stdout = io.MultiWriter(os.Stderr)
	cmd.Stderr = io.MultiWriter(os.Stderr)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start libkrun helper: %w", err)
	}
	m := &machine{cmd: cmd, socket: spec.Socket, done: make(chan struct{})}
	go m.reap()
	if err := ctx.Err(); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.Stop(stopCtx)
		return nil, err
	}
	return m, nil
}

type machine struct {
	cmd    *exec.Cmd
	socket string
	done   chan struct{}
	mu     sync.Mutex
	exit   Exit
	err    error
	stop   sync.Once
}

func (m *machine) reap() {
	err := m.cmd.Wait()
	m.mu.Lock()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			m.exit.Code = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				m.exit.Signal = status.Signal().String()
			}
		} else {
			m.err = err
		}
	}
	m.mu.Unlock()
	close(m.done)
}

func (m *machine) OpenControl(ctx context.Context) (io.ReadWriteCloser, error) {
	for {
		conn, err := net.DialTimeout("unix", m.socket, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect guest control: %w", ctx.Err())
		case <-m.done:
			exit, waitErr := m.Wait(context.Background())
			return nil, fmt.Errorf("VM exited before control ready (exit %d): %v", exit.Code, waitErr)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m *machine) Wait(ctx context.Context) (Exit, error) {
	select {
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	case <-m.done:
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.exit, m.err
	}
}

func (m *machine) Stop(ctx context.Context) error {
	m.stop.Do(func() { _ = m.cmd.Process.Signal(syscall.SIGTERM) })
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		_ = m.cmd.Process.Kill()
		select {
		case <-m.done:
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("force-stop VM: %w", ctx.Err())
		}
	}
}
