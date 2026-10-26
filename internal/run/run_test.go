package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ctx-lab/wolfcow/internal/network"
	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

type controlMachine struct {
	files        workspace.Snapshot
	manifest     []string
	binaryOnExec bool
}

func (m *controlMachine) OpenControl(context.Context) (io.ReadWriteCloser, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		_ = protocol.Write(guest, protocol.Message{Type: "ready", Major: protocol.Major, Kernel: "test-kernel", Operations: []string{"exec", "read", "write", "diff"}})
		request, err := protocol.Read(guest)
		if err != nil {
			return
		}
		respond := func(message protocol.Message) { message.ID = request.ID; _ = protocol.Write(guest, message) }
		switch request.Type {
		case "file.write":
			m.files[request.Path] = append([]byte(nil), request.Data...)
			respond(protocol.Message{Type: "file.written", Path: request.Path})
		case "file.read":
			respond(protocol.Message{Type: "file.data", Path: request.Path, Data: m.files[request.Path]})
		case "exec.start":
			m.files["note.txt"] = []byte("changed\n")
			m.files["new.txt"] = []byte("new\n")
			if m.binaryOnExec {
				m.files["binary.dat"] = []byte{0, 1, 2}
			}
			delete(m.files, "gone.txt")
			respond(protocol.Message{Type: "exec.stdout", Data: []byte("ran\n")})
			code := 7
			respond(protocol.Message{Type: "exec.exit", ExitCode: &code})
		case "workspace.diff":
			paths := m.manifest
			if paths == nil {
				for name := range m.files {
					paths = append(paths, name)
				}
				sort.Strings(paths)
			}
			respond(protocol.Message{Type: "workspace.files", Files: paths})
		default:
			respond(protocol.Message{Type: "error", Code: "BAD_REQUEST", Error: fmt.Sprintf("unknown %s", request.Type)})
		}
	}()
	return host, nil
}
func (*controlMachine) Wait(context.Context) (vmm.Exit, error) { return vmm.Exit{}, nil }
func (*controlMachine) Stop(context.Context) error             { return nil }

func TestRunOnMachineReturnsResultForCommandFailure(t *testing.T) {
	before := workspace.Snapshot{"note.txt": []byte("original\n"), "gone.txt": []byte("gone\n")}
	machine := &controlMachine{files: workspace.Snapshot{"note.txt": []byte("original\n"), "gone.txt": []byte("gone\n")}}
	spec := defaults(Spec{Argv: []string{"/bin/false"}, Writes: workspace.Snapshot{"host-note.txt": []byte("host\n")}})
	var stdout, stderr bytes.Buffer
	result, err := runOnMachine(context.Background(), machine, before, spec, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Kernel != "test-kernel" || stdout.String() != "ran\n" {
		t.Fatalf("result=%+v stdout=%q", result, stdout.String())
	}
	if len(result.Changes) != 4 || string(result.ChangedFiles["new.txt"]) != "new\n" {
		t.Fatalf("changes=%+v files=%v", result.Changes, result.ChangedFiles)
	}
	if _, exists := result.ChangedFiles["gone.txt"]; exists {
		t.Fatal("deleted file returned as contents")
	}
	if !strings.Contains(result.Diff, "-original\n+changed\n") {
		t.Fatalf("diff=%q", result.Diff)
	}
	if string(before["note.txt"]) != "original\n" {
		t.Fatal("source snapshot mutated")
	}
}

func TestRunOnMachineRejectsUnsafeGuestInventory(t *testing.T) {
	machine := &controlMachine{files: workspace.Snapshot{}, manifest: []string{"../escape"}}
	spec := defaults(Spec{Argv: []string{"/bin/true"}})
	_, err := runOnMachine(context.Background(), machine, workspace.Snapshot{}, spec, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unsafe guest file") {
		t.Fatalf("got %v", err)
	}
}

func TestRunOnMachineReturnsBinaryChanges(t *testing.T) {
	machine := &controlMachine{files: workspace.Snapshot{}, binaryOnExec: true}
	spec := defaults(Spec{Argv: []string{"/bin/true"}})
	result, err := runOnMachine(context.Background(), machine, workspace.Snapshot{}, spec, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.ChangedFiles["binary.dat"], []byte{0, 1, 2}) {
		t.Fatalf("binary contents = %v", result.ChangedFiles["binary.dat"])
	}
	if !strings.Contains(result.Diff, "Binary files /dev/null and b/binary.dat differ\n") {
		t.Fatalf("diff = %q", result.Diff)
	}
}

func TestInvalidLimitsRejectedBeforeBoot(t *testing.T) {
	if err := validate(Spec{CPUs: 1, MemoryMiB: 512, ExecTimeout: 6 * time.Minute, OutputMax: 1024}); err == nil {
		t.Fatal("accepted excessive timeout")
	}
}

func TestInvalidNetworkPolicyRejectedBeforeBoot(t *testing.T) {
	spec := defaults(Spec{Network: network.Policy{TCP: []network.Destination{{Host: "127.0.0.1", Port: 443}}}})
	if err := validate(spec); err == nil {
		t.Fatal("accepted raw-IP network destination")
	}
}
