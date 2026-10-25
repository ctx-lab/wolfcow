//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ctx-lab/wolfcow/internal/protocol"
)

type sockaddrVM struct {
	family   uint16
	reserved uint16
	port     uint32
	cid      uint32
	flags    uint8
	zero     [3]uint8
}

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "wolfcow-guest:", err)
		os.Exit(1)
	}
}

func serve() error {
	fd, err := syscall.Socket(syscall.AF_VSOCK, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	addr := sockaddrVM{family: syscall.AF_VSOCK, port: protocol.ControlPort, cid: ^uint32(0)}
	_, _, errno := syscall.RawSyscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&addr)), unsafe.Sizeof(addr))
	if errno != 0 {
		return fmt.Errorf("bind vsock: %w", errno)
	}
	if err := syscall.Listen(fd, 4); err != nil {
		return err
	}
	for {
		connected, _, errno := syscall.RawSyscall6(syscall.SYS_ACCEPT4, uintptr(fd), 0, 0, syscall.SOCK_CLOEXEC, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return fmt.Errorf("accept vsock: %w", errno)
		}
		conn := os.NewFile(connected, "control")
		handle(conn)
		conn.Close()
	}
}

func handle(conn *os.File) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		return
	}
	kernel := chars(uname.Release[:])
	if err := protocol.Write(conn, protocol.Message{
		Type: "ready", Major: protocol.Major, Minor: protocol.Minor,
		Operations: []string{"exec", "read", "write", "diff"}, Kernel: kernel,
	}); err != nil {
		return
	}
	for {
		request, err := protocol.Read(conn)
		if err != nil {
			return
		}
		if request.ID == "" || len(request.ID) > 128 {
			_ = protocol.Write(conn, protocol.Message{Type: "error", ID: request.ID, Code: "BAD_REQUEST", Error: "invalid request id"})
			return
		}
		switch request.Type {
		case "exec.start":
			if len(request.Argv) == 0 || request.Argv[0] == "" {
				_ = protocol.Write(conn, protocol.Message{Type: "error", ID: request.ID, Code: "BAD_REQUEST", Error: "expected argv"})
				return
			}
			execute(conn, request)
		case "file.read", "file.write", "workspace.diff":
			handleFile(conn, request)
		default:
			_ = protocol.Write(conn, protocol.Message{Type: "error", ID: request.ID, Code: "BAD_REQUEST", Error: "unknown operation"})
		}
		return // One operation per control connection.
	}
}

func chars(in []int8) string {
	var out []byte
	for _, c := range in {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func execute(conn *os.File, request protocol.Message) {
	var writeMu sync.Mutex
	write := func(m protocol.Message) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return protocol.Write(conn, m)
	}
	bad := func(message string) {
		_ = write(protocol.Message{Type: "error", ID: request.ID, Code: "BAD_REQUEST", Error: message})
	}
	if len(request.ID) > 128 || len(request.Argv) > 128 || len(request.Env) > 128 {
		bad("request exceeds argument limit")
		return
	}
	if request.Dir != "" && !strings.HasPrefix(request.Dir, "/") {
		bad("working directory must be absolute")
		return
	}
	for _, entry := range request.Env {
		if !strings.Contains(entry, "=") || strings.HasPrefix(entry, "=") || strings.ContainsRune(entry, 0) {
			bad("invalid environment entry")
			return
		}
	}
	deadline := time.UnixMilli(request.DeadlineMS)
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > time.Duration(protocol.MaxExecMillis)*time.Millisecond {
		bad("deadline must be within five minutes")
		return
	}
	limit := request.OutputMax
	if limit <= 0 || limit > protocol.MaxOutput {
		bad("invalid output limit")
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	cmd := exec.Command(request.Argv[0], request.Argv[1:]...)
	cmd.Dir = request.Dir
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, request.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = write(protocol.Message{Type: "error", ID: request.ID, Code: "INTERNAL", Error: err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = write(protocol.Message{Type: "error", ID: request.ID, Code: "INTERNAL", Error: err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		_ = write(protocol.Message{Type: "error", ID: request.ID, Code: "BAD_REQUEST", Error: err.Error()})
		return
	}
	var wg sync.WaitGroup
	var countMu sync.Mutex
	var total int64
	var exceeded bool
	stream := func(r io.Reader, kind string) {
		defer wg.Done()
		buf := make([]byte, 16<<10)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				countMu.Lock()
				left := limit - total
				send := int64(n)
				if send > left {
					send = left
					exceeded = true
					cancel()
				}
				total += send
				countMu.Unlock()
				if send > 0 {
					if err := write(protocol.Message{Type: kind, ID: request.ID, Data: append([]byte(nil), buf[:send]...)}); err != nil {
						cancel()
						return
					}
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	wg.Add(2)
	go stream(stdout, "exec.stdout")
	go stream(stderr, "exec.stderr")
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-finished:
				return
			default:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-finished:
		}
	}()
	go func() {
		m, err := protocol.Read(conn)
		if err != nil || m.Type == "exec.cancel" && m.ID == request.ID {
			cancel()
		}
	}()
	wg.Wait()
	waitErr := cmd.Wait()
	close(finished)
	exit := protocol.Message{Type: "exec.exit", ID: request.ID}
	countMu.Lock()
	wasExceeded := exceeded
	countMu.Unlock()
	switch {
	case wasExceeded:
		exit.Status = "OUTPUT_LIMIT"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		exit.Status = "DEADLINE_EXCEEDED"
	case errors.Is(ctx.Err(), context.Canceled):
		exit.Status = "CANCELLED"
	case waitErr == nil:
		code := 0
		exit.ExitCode = &code
	default:
		var e *exec.ExitError
		if errors.As(waitErr, &e) {
			code := e.ExitCode()
			exit.ExitCode = &code
			if status, ok := e.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				exit.Signal = status.Signal().String()
			}
		} else {
			exit.Status = "INTERNAL"
		}
	}
	_ = write(exit)
}
