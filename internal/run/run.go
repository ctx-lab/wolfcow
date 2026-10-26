// Package run owns one disposable VM execution against a copied workspace.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/ctx-lab/wolfcow/internal/guest"
	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/vmm"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

type Spec struct {
	Workspace   string
	Argv        []string
	Writes      workspace.Snapshot // Optional pre-exec writes sent over the guest protocol.
	CPUs        uint8
	MemoryMiB   uint32
	ExecTimeout time.Duration
	OutputMax   int64
}

type Result struct {
	Kernel       string
	ExitCode     int
	Signal       string
	Status       string
	Changes      []protocol.Change
	ChangedFiles workspace.Snapshot // Created and modified contents; never applied to source.
	Diff         string
}

type Runner struct {
	Bundle  string
	Backend vmm.Backend
}

func (r Runner) Run(ctx context.Context, spec Spec, stdout, stderr io.Writer) (result Result, err error) {
	if r.Backend == nil && (runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
		return result, fmt.Errorf("one-shot runs require Apple Silicon macOS")
	}
	if spec.Workspace == "" || len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return result, fmt.Errorf("workspace and guest command required")
	}
	spec = defaults(spec)
	if err := validate(spec); err != nil {
		return result, err
	}
	before, err := workspace.ReadSnapshot(spec.Workspace)
	if err != nil {
		return result, fmt.Errorf("workspace input: %w", err)
	}
	bundle := r.Bundle
	if bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			return result, err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return result, err
		}
		bundle = filepath.Join(filepath.Dir(exe), "runtime")
	}
	assets, err := vmm.PrepareAgent(bundle)
	if err != nil {
		return result, fmt.Errorf("prepare guest: %w", err)
	}
	defer func() { err = errors.Join(err, assets.Cleanup()) }()
	if err := workspace.WriteSnapshot(filepath.Join(assets.Rootfs, "work"), before); err != nil {
		return result, fmt.Errorf("stage workspace: %w", err)
	}
	backend := r.Backend
	if backend == nil {
		backend = vmm.Libkrun{Bundle: bundle}
	}
	bootCtx, cancelBoot := context.WithTimeout(ctx, 15*time.Second)
	machine, err := backend.Boot(bootCtx, vmm.BootSpec{Rootfs: assets.Rootfs, Socket: assets.Socket, CPUs: spec.CPUs, MemoryMiB: spec.MemoryMiB})
	cancelBoot()
	if err != nil {
		return result, fmt.Errorf("boot guest: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err = errors.Join(err, machine.Stop(stopCtx))
		_, waitErr := machine.Wait(stopCtx)
		err = errors.Join(err, waitErr)
	}()
	return runOnMachine(ctx, machine, before, spec, stdout, stderr)
}

func defaults(spec Spec) Spec {
	if spec.CPUs == 0 {
		spec.CPUs = 1
	}
	if spec.MemoryMiB == 0 {
		spec.MemoryMiB = 512
	}
	if spec.ExecTimeout == 0 {
		spec.ExecTimeout = 30 * time.Second
	}
	if spec.OutputMax == 0 {
		spec.OutputMax = protocol.MaxOutput
	}
	return spec
}

func validate(spec Spec) error {
	if spec.CPUs > 8 || spec.MemoryMiB < 128 || spec.MemoryMiB > 8192 {
		return fmt.Errorf("VM resource limit out of range")
	}
	if spec.ExecTimeout <= 0 || spec.ExecTimeout > time.Duration(protocol.MaxExecMillis)*time.Millisecond {
		return fmt.Errorf("exec timeout out of range")
	}
	if spec.OutputMax <= 0 || spec.OutputMax > protocol.MaxOutput {
		return fmt.Errorf("output limit out of range")
	}
	if len(spec.Writes) > protocol.MaxFiles {
		return fmt.Errorf("pre-exec writes exceed file limit")
	}
	for name, data := range spec.Writes {
		if err := workspace.SafePath(name); err != nil {
			return err
		}
		if len(data) > protocol.MaxFile {
			return fmt.Errorf("pre-exec write too large: %s", name)
		}
	}
	return nil
}

func runOnMachine(ctx context.Context, machine vmm.Machine, before workspace.Snapshot, spec Spec, stdout, stderr io.Writer) (Result, error) {
	var result Result
	paths := make([]string, 0, len(spec.Writes))
	for name := range spec.Writes {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		data := spec.Writes[name]
		if _, err := guest.Operation(ctx, machine, protocol.Message{Type: "file.write", ID: "write", Path: name, Data: data}, "file.written"); err != nil {
			return result, fmt.Errorf("write guest file %s: %w", name, err)
		}
		readBack, err := guest.Operation(ctx, machine, protocol.Message{Type: "file.read", ID: "read", Path: name}, "file.data")
		if err != nil {
			return result, fmt.Errorf("verify guest file %s: %w", name, err)
		}
		if readBack.Path != name || !bytes.Equal(readBack.Data, data) {
			return result, fmt.Errorf("guest file write verification failed: %s", name)
		}
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, 15*time.Second)
	conn, ready, err := guest.Open(readyCtx, machine)
	cancelReady()
	if err != nil {
		return result, fmt.Errorf("guest ready: %w", err)
	}
	result.Kernel = ready.Kernel
	execResult, execErr := guest.Exec(ctx, conn, ready, guest.ExecSpec{Argv: spec.Argv, Dir: "/work", Timeout: spec.ExecTimeout, OutputMax: spec.OutputMax}, stdout, stderr)
	_ = conn.Close()
	result.ExitCode, result.Signal, result.Status = execResult.Code, execResult.Signal, execResult.Status
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if execErr != nil {
		return result, fmt.Errorf("guest exec: %w", execErr)
	}
	files, err := collect(ctx, machine)
	if err != nil {
		return result, fmt.Errorf("collect workspace: %w", err)
	}
	result.Changes, err = workspace.Changes(before, files)
	if err != nil {
		return result, err
	}
	result.ChangedFiles = make(workspace.Snapshot)
	for _, change := range result.Changes {
		if change.Kind != "deleted" {
			result.ChangedFiles[change.Path] = files[change.Path]
		}
	}
	result.Diff, err = workspace.Unified(before, files, result.Changes)
	if err != nil {
		return result, err
	}
	return result, nil
}

func collect(ctx context.Context, machine vmm.Machine) (workspace.Snapshot, error) {
	manifest, err := guest.Operation(ctx, machine, protocol.Message{Type: "workspace.diff", ID: "diff"}, "workspace.files")
	if err != nil {
		return nil, err
	}
	if len(manifest.Files) > protocol.MaxFiles {
		return nil, fmt.Errorf("guest file list exceeds limit")
	}
	files := make(workspace.Snapshot, len(manifest.Files))
	var total int
	for _, name := range manifest.Files {
		if err := workspace.SafePath(name); err != nil {
			return nil, fmt.Errorf("unsafe guest file: %w", err)
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("duplicate guest file %q", name)
		}
		m, err := guest.Operation(ctx, machine, protocol.Message{Type: "file.read", ID: "pull", Path: name}, "file.data")
		if err != nil {
			return nil, err
		}
		if m.Path != name || len(m.Data) > protocol.MaxFile {
			return nil, fmt.Errorf("invalid guest file response")
		}
		total += len(m.Data)
		if total > protocol.MaxWorkspace {
			return nil, fmt.Errorf("guest workspace exceeds size limit")
		}
		files[name] = m.Data
	}
	return files, nil
}
