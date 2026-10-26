package spike

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ctx-lab/wolfcow/internal/network"
	"github.com/ctx-lab/wolfcow/internal/vmm"
)

// RunNet checks the virtio-net link and synthetic explicit-proxy policy.
func RunNet(ctx context.Context, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("network link probe requires Apple Silicon macOS")
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
	assets, err := vmm.PrepareAgent(bundle)
	if err != nil {
		return err
	}
	defer assets.Cleanup()
	netSocket := filepath.Join(filepath.Dir(assets.Socket), "net.sock")
	proxy, err := network.StartGVProxy(ctx, bundle, netSocket, network.Policy{})
	if err != nil {
		return err
	}
	defer proxy.Stop()
	bootCtx, cancelBoot := context.WithTimeout(ctx, 15*time.Second)
	machine, err := (vmm.Libkrun{Bundle: bundle}).Boot(bootCtx, vmm.BootSpec{
		Rootfs: assets.Rootfs, Socket: assets.Socket, NetSocket: netSocket, CPUs: 1, MemoryMiB: 512,
	})
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
	readyCtx, cancelReady := context.WithTimeout(ctx, 15*time.Second)
	conn, ready, err := awaitReady(readyCtx, machine)
	cancelReady()
	if err != nil {
		return err
	}
	defer conn.Close()
	// All external destinations remain denied. The sole allowed proxy request
	// returns a static response from the gateway and makes no host dial.
	command := `set -eu
/bin/busybox ip -4 addr show dev eth0
/bin/busybox ip route
health=$(/bin/busybox wget -Y off -T 5 -qO- http://192.168.127.1:80/wolfcow/health)
test "$health" = 'wolfcow network ready'
echo 'gateway health passed'
response=$(http_proxy=http://192.168.127.1:80 /bin/busybox wget -Y on -T 5 -qO- http://allowed.wolfcow.invalid/probe)
test "$response" = '{"ok":true,"endpoint":"synthetic"}'
echo 'synthetic endpoint allowed'
expect_forbidden() {
  if denial=$(http_proxy=http://192.168.127.1:80 /bin/busybox wget -Y on -T 3 -O- "$1" 2>&1); then
    echo "$2 bypassed policy"; return 1
  fi
  case "$denial" in *'403 Forbidden'*) echo "$2 denied";; *) echo "$denial"; return 1;; esac
}
expect_forbidden http://blocked.example/probe 'blocked hostname'
expect_forbidden http://allowed.wolfcow.invalid.evil/probe 'hostname suffix'
expect_forbidden http://allowed.wolfcow.invalid:8080/probe 'other port'
expect_forbidden http://198.51.100.1/probe 'raw IP through proxy'
if denial=$(/bin/busybox wget -Y off -T 3 -O- http://198.51.100.1:80/ 2>&1); then
  echo 'direct raw IP bypassed policy'; exit 1
fi
case "$denial" in *'Connection refused'*) echo 'direct raw IP denied';; *) echo "$denial"; exit 1;; esac`
	if err := execInDir(ctx, conn, ready, []string{"/bin/sh", "-c", command}, "/", stdout, stderr); err != nil {
		// This is a development conformance probe, so always print its failure.
		// Wrapping GuestExitError would make cmd/wolfcow propagate only the exit
		// code and hide the host-side policy diagnostics.
		return fmt.Errorf("guest network probe failed: %v\nproxy log:\n%s", err, proxy.LogTail())
	}
	return nil
}
