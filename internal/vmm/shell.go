// Package vmm manages the bundled macOS VM runtime and disposable rootfs.
package vmm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
)

const imageSHA256 = "6bf491907b705caa5fc65773fbbc1d0954530b6c455d4446f670a4c1fdcea489"

// RunShell starts the bundled helper and waits so the disposable rootfs can be removed.
// An explicit rootfs is copied into disposable storage before the VM starts.
func RunShell(rootfs string) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("v0.0.1 requires Apple Silicon macOS")
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
	helper := filepath.Join(bundle, "wolfcow-krun")
	for _, name := range []string{"wolfcow-krun", "libkrun.1.dylib", "libkrunfw.5.dylib"} {
		info, err := os.Stat(filepath.Join(bundle, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("runtime incomplete (%s); from source, run task build", name)
		}
	}
	staging, err := os.MkdirTemp("", "wolfcow-shell-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if rootfs == "" {
		image := filepath.Join(bundle, "rootfs.tar.gz")
		if err := verifyImage(image); err != nil {
			return err
		}
		if err := extractImage(image, staging); err != nil {
			return fmt.Errorf("prepare Linux image: %w", err)
		}
	} else {
		rootfs, err = filepath.Abs(rootfs)
		if err != nil {
			return err
		}
		rootfs, err = filepath.EvalSymlinks(rootfs)
		if err != nil {
			return fmt.Errorf("rootfs: %w", err)
		}
		info, err := os.Stat(rootfs)
		if err != nil {
			return err
		}
		if !info.IsDir() || rootfs == "/" {
			return fmt.Errorf("rootfs must be a disposable directory other than /")
		}
		if err := copyRootfs(rootfs, staging); err != nil {
			return fmt.Errorf("copy rootfs: %w", err)
		}
	}
	cmd := exec.Command(helper, staging)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Do not pass host credentials or loader overrides to the helper.
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start VM: %w", err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	defer signal.Stop(signals)
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	return cmd.Wait()
}

func verifyImage(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("default Linux image missing; from source, run task build: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, 32<<20)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != imageSHA256 {
		return fmt.Errorf("default Linux image checksum mismatch; rebuild or reinstall wolfcow")
	}
	return nil
}
