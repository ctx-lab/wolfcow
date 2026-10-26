package guest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
)

type readyMachine struct {
	conn   io.ReadWriteCloser
	opened chan struct{}
}

func (m *readyMachine) OpenControl(context.Context) (io.ReadWriteCloser, error) {
	close(m.opened)
	return m.conn, nil
}
func (*readyMachine) Wait(context.Context) (vmm.Exit, error) { return vmm.Exit{}, nil }
func (*readyMachine) Stop(context.Context) error             { return nil }

func TestOpenClosesStalledHandshakeOnCancellation(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	machine := &readyMachine{conn: host, opened: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, _, err := Open(ctx, machine)
		done <- err
	}()
	<-machine.opened
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled && !errors.Is(err, context.Canceled) {
			t.Fatalf("Open error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ready handshake did not stop on cancellation")
	}
}

func TestOpenLeavesReadyConnectionOpenAfterContextCancellation(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	go func() {
		_ = protocol.Write(peer, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test"})
	}()
	ctx, cancel := context.WithCancel(context.Background())
	machine := &readyMachine{conn: host, opened: make(chan struct{})}
	conn, _, err := Open(ctx, machine)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	go func() { _, _ = peer.Write([]byte("x")) }()
	data := make([]byte, 1)
	if _, err := io.ReadFull(conn, data); err != nil || data[0] != 'x' {
		t.Fatalf("ready connection closed after Open: %q, %v", data, err)
	}
}

func TestExecReportsCommandExitAsResult(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	done := make(chan error, 1)
	go func() {
		request, err := protocol.Read(guest)
		if err != nil {
			done <- err
			return
		}
		if len(request.Env) != 1 || request.Env[0] != "HTTPS_PROXY=http://192.168.127.1:80" {
			done <- fmt.Errorf("guest env = %v", request.Env)
			return
		}
		code := 7
		if err := protocol.Write(guest, protocol.Message{Type: "exec.stdout", ID: request.ID, Data: []byte("output")}); err != nil {
			done <- err
			return
		}
		done <- protocol.Write(guest, protocol.Message{Type: "exec.exit", ID: request.ID, ExitCode: &code})
	}()
	ready := protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test", Operations: []string{"exec"}}
	var stdout bytes.Buffer
	result, err := Exec(context.Background(), host, ready, ExecSpec{Argv: []string{"/bin/false"}, Dir: "/work", Env: []string{"HTTPS_PROXY=http://192.168.127.1:80"}, Timeout: time.Second, OutputMax: 1024}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 7 || result.Kernel != "test" || stdout.String() != "output" {
		t.Fatalf("result=%+v output=%q", result, stdout.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
