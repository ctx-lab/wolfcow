package spike

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ctx-lab/wolfcow/internal/protocol"
)

func TestFileOperationRejectsWrongResponseID(t *testing.T) {
	host, guest := net.Pipe()
	defer guest.Close()
	go func() {
		_ = protocol.Write(guest, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test", Operations: []string{"read"}})
		_, _ = protocol.Read(guest)
		_ = protocol.Write(guest, protocol.Message{Type: "file.data", ID: "wrong", Path: "note", Data: []byte("x")})
	}()
	machine := &retryMachine{conns: []io.ReadWriteCloser{host}}
	_, err := fileOperation(context.Background(), machine, protocol.Message{Type: "file.read", ID: "request", Path: "note"}, "file.data")
	if err == nil || !strings.Contains(err.Error(), "response id") {
		t.Fatalf("got %v", err)
	}
}

func TestFileOperationCancellationClosesControl(t *testing.T) {
	host, guest := net.Pipe()
	defer guest.Close()
	requestSeen := make(chan struct{})
	go func() {
		_ = protocol.Write(guest, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test", Operations: []string{"diff"}})
		_, _ = protocol.Read(guest)
		close(requestSeen)
	}()
	machine := &retryMachine{conns: []io.ReadWriteCloser{host}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-requestSeen; cancel() }()
	start := time.Now()
	_, err := fileOperation(ctx, machine, protocol.Message{Type: "workspace.diff", ID: "request"}, "workspace.files")
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation result %v after %v", err, time.Since(start))
	}
}
