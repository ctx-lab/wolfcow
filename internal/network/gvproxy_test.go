package network

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGVProxyRejectsUnsafeSocketSetup(t *testing.T) {
	if _, err := StartGVProxy(context.Background(), "/missing", "relative.sock", Policy{}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path: %v", err)
	}
	path := filepath.Join(t.TempDir(), "occupied.sock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartGVProxy(context.Background(), "/missing", path, Policy{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("occupied path: %v", err)
	}
}
