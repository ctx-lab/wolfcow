package spike

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

// RunConformance exercises the Stage B control contract against macOS libkrun.
func RunConformance(ctx context.Context, source string, out io.Writer) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("Stage B requires Apple Silicon macOS")
	}
	files, err := workspace.ReadSnapshot(source)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := filepath.Join(filepath.Dir(exe), "runtime")
	assets, err := vmm.PrepareAgent(bundle)
	if err != nil {
		return err
	}
	defer assets.Cleanup()
	if err := workspace.WriteSnapshot(filepath.Join(assets.Rootfs, "work"), files); err != nil {
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
	checkRejected := func(label string, request protocol.Message) error {
		_, err := fileOperation(ctx, machine, request, "file.written")
		if err == nil || !strings.Contains(err.Error(), "BAD_REQUEST") {
			return fmt.Errorf("%s: wanted BAD_REQUEST, got %v", label, err)
		}
		_, _ = fmt.Fprintf(out, "PASS %s\n", label)
		return nil
	}
	if err := checkRejected("path traversal", protocol.Message{Type: "file.write", ID: "escape", Path: "../escape", Data: []byte("x")}); err != nil {
		return err
	}
	if err := checkRejected("absolute path", protocol.Message{Type: "file.write", ID: "absolute", Path: "/tmp/escape", Data: []byte("x")}); err != nil {
		return err
	}
	if err := checkRejected("file size limit", protocol.Message{Type: "file.write", ID: "large", Path: "large", Data: make([]byte, protocol.MaxFile+1)}); err != nil {
		return err
	}
	if err := conformanceExec(ctx, machine, "/bin/ln", "-s", "/etc/passwd", "/work/escape-link"); err != nil {
		return err
	}
	_, err = fileOperation(ctx, machine, protocol.Message{Type: "file.read", ID: "link", Path: "escape-link"}, "file.data")
	if err == nil || !strings.Contains(err.Error(), "BAD_REQUEST") {
		return fmt.Errorf("symlink escape: wanted BAD_REQUEST, got %v", err)
	}
	_, _ = fmt.Fprintln(out, "PASS symlink escape")
	if err := conformanceExec(ctx, machine, "/bin/rm", "/work/escape-link"); err != nil {
		return err
	}
	if err := checkMalformedFrame(ctx, machine); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "PASS malformed frame")
	if err := checkBoundedExec(ctx, machine, 1024, "/bin/sh", "-c", "dd if=/dev/zero bs=2048 count=1 2>/dev/null"); err != nil {
		return fmt.Errorf("output limit: %w", err)
	}
	_, _ = fmt.Fprintln(out, "PASS output limit")
	if err := checkCancelledExec(ctx, machine); err != nil {
		return fmt.Errorf("cancellation: %w", err)
	}
	_, _ = fmt.Fprintln(out, "PASS cancellation")
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err := machine.Stop(stopCtx); err != nil {
		return err
	}
	if _, err := machine.Wait(stopCtx); err != nil {
		return err
	}
	if err := machine.Stop(stopCtx); err != nil {
		return err
	}
	if err := assets.Cleanup(); err != nil {
		return err
	}
	if _, err := os.Stat(assets.Rootfs); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("teardown left rootfs: %v", err)
	}
	_, _ = fmt.Fprintln(out, "PASS teardown")
	return nil
}

func conformanceExec(ctx context.Context, machine vmm.Machine, argv ...string) error {
	readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, ready, err := awaitReady(readyCtx, machine)
	if err != nil {
		return err
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	return execInDir(ctx, conn, ready, argv, "/work", io.Discard, io.Discard)
}

func conformanceConn(ctx context.Context, machine vmm.Machine) (io.ReadWriteCloser, error) {
	readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, ready, err := awaitReady(readyCtx, machine)
	if err != nil {
		return nil, err
	}
	if ready.Type != "ready" || ready.Major != protocol.Major {
		conn.Close()
		return nil, fmt.Errorf("unsupported guest protocol")
	}
	return conn, nil
}

func checkMalformedFrame(ctx context.Context, machine vmm.Machine) error {
	conn, err := conformanceConn(ctx, machine)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Unknown fields are rejected by the shared decoder; the guest closes this connection.
	data := []byte(`{"type":"file.read","id":"bad","path":"note.txt","unexpected":true}`)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	if timed, ok := conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = timed.SetReadDeadline(time.Now().Add(2 * time.Second))
	}
	_, err = protocol.Read(conn)
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf("malformed frame response: %v", err)
	}
	return nil
}

func checkBoundedExec(ctx context.Context, machine vmm.Machine, limit int64, argv ...string) error {
	conn, err := conformanceConn(ctx, machine)
	if err != nil {
		return err
	}
	defer conn.Close()
	request := protocol.Message{Type: "exec.start", ID: "limit", Argv: argv, Dir: "/work", DeadlineMS: time.Now().Add(10 * time.Second).UnixMilli(), OutputMax: limit}
	if timed, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
		_ = timed.SetDeadline(time.Now().Add(12 * time.Second))
	}
	if err := protocol.Write(conn, request); err != nil {
		return err
	}
	var received int64
	for {
		m, err := protocol.Read(conn)
		if err != nil {
			return err
		}
		if m.ID != request.ID {
			return fmt.Errorf("wrong response id")
		}
		switch m.Type {
		case "exec.stdout", "exec.stderr":
			received += int64(len(m.Data))
			if received > limit {
				return fmt.Errorf("guest exceeded output limit")
			}
		case "exec.exit":
			if m.Status != "OUTPUT_LIMIT" {
				return fmt.Errorf("exit status %q", m.Status)
			}
			return nil
		case "error":
			return fmt.Errorf("guest %s: %s", m.Code, m.Error)
		default:
			return fmt.Errorf("unexpected response %q", m.Type)
		}
	}
}

func checkCancelledExec(ctx context.Context, machine vmm.Machine) error {
	conn, err := conformanceConn(ctx, machine)
	if err != nil {
		return err
	}
	defer conn.Close()
	if timed, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
		_ = timed.SetDeadline(time.Now().Add(12 * time.Second))
	}
	request := protocol.Message{Type: "exec.start", ID: "cancel", Argv: []string{"/bin/sh", "-c", "sleep 10"}, Dir: "/work", DeadlineMS: time.Now().Add(11 * time.Second).UnixMilli(), OutputMax: 1024}
	if err := protocol.Write(conn, request); err != nil {
		return err
	}
	if err := protocol.Write(conn, protocol.Message{Type: "exec.cancel", ID: request.ID}); err != nil {
		return err
	}
	for {
		m, err := protocol.Read(conn)
		if err != nil {
			return err
		}
		if m.ID != request.ID {
			return fmt.Errorf("wrong response id")
		}
		if m.Type == "exec.exit" {
			if m.Status != "CANCELLED" {
				return fmt.Errorf("exit status %q", m.Status)
			}
			return nil
		}
		if m.Type == "error" {
			return fmt.Errorf("guest %s: %s", m.Code, m.Error)
		}
	}
}
