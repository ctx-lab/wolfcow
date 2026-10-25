package spike

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

type retryMachine struct{ conns []io.ReadWriteCloser }

func (m *retryMachine) OpenControl(context.Context) (io.ReadWriteCloser, error) {
	if len(m.conns) == 0 {
		return nil, fmt.Errorf("no more connections")
	}
	conn := m.conns[0]
	m.conns = m.conns[1:]
	return conn, nil
}
func (*retryMachine) Wait(context.Context) (vmm.Exit, error) { return vmm.Exit{}, nil }
func (*retryMachine) Stop(context.Context) error             { return nil }

func TestReadyRetriesEarlyEOF(t *testing.T) {
	firstHost, firstGuest := net.Pipe()
	firstGuest.Close() // A connection accepted before the guest listener is ready.
	secondHost, secondGuest := net.Pipe()
	defer secondGuest.Close()
	go func() {
		_ = protocol.Write(secondGuest, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test", Operations: []string{"exec"}})
	}()
	m := &retryMachine{conns: []io.ReadWriteCloser{firstHost, secondHost}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, ready, err := awaitReady(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if ready.Type != "ready" || ready.Kernel != "test" {
		t.Fatalf("ready = %+v", ready)
	}
}

func TestControlSeparatesOutputAndExitCode(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	finished := make(chan error, 1)
	go func() {
		if err := protocol.Write(guest, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test-kernel", Operations: []string{"exec"}}); err != nil {
			finished <- err
			return
		}
		request, err := protocol.Read(guest)
		if err != nil {
			finished <- err
			return
		}
		if request.Type != "exec.start" || len(request.Argv) != 1 || request.Argv[0] != "/bin/false" {
			finished <- errors.New("unexpected exec request")
			return
		}
		code := 7
		for _, m := range []protocol.Message{
			{Type: "exec.stdout", ID: request.ID, Data: []byte("out")},
			{Type: "exec.stderr", ID: request.ID, Data: []byte("err")},
			{Type: "exec.exit", ID: request.ID, ExitCode: &code},
		} {
			if err := protocol.Write(guest, m); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	var stdout, stderr bytes.Buffer
	err := runControl(context.Background(), host, []string{"/bin/false"}, &stdout, &stderr)
	var exit GuestExitError
	if !errors.As(err, &exit) || exit.Code != 7 {
		t.Fatalf("exit = %v, want status 7", err)
	}
	if stdout.String() != "out" || stderr.String() != "guest kernel: test-kernel\nerr" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
