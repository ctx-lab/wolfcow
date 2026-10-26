package vmm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootRejectsInvalidNetworkEndpointBeforeStart(t *testing.T) {
	root := t.TempDir()
	spec := BootSpec{Rootfs: root, Socket: filepath.Join(root, "control.sock"), NetSocket: "relative.sock", CPUs: 1, MemoryMiB: 512}
	if _, err := (Libkrun{Bundle: root}).Boot(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative endpoint: %v", err)
	}
	spec.NetSocket = filepath.Join(root, "missing.sock")
	if _, err := (Libkrun{Bundle: root}).Boot(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "network socket") {
		t.Fatalf("missing endpoint: %v", err)
	}
}
