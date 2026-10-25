package vmm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type AgentAssets struct {
	Rootfs  string
	Socket  string
	Cleanup func() error
}

// PrepareAgent copies the verified base image and bundled static guest agent
// into disposable storage. The host control socket stays outside the rootfs.
func PrepareAgent(bundle string) (AgentAssets, error) {
	image := filepath.Join(bundle, "rootfs.tar.gz")
	if err := verifyImage(image); err != nil {
		return AgentAssets{}, err
	}
	work, err := os.MkdirTemp("", "wolfcow-agent-")
	if err != nil {
		return AgentAssets{}, err
	}
	cleanup := func() error { return os.RemoveAll(work) }
	root := filepath.Join(work, "rootfs")
	if err := os.Mkdir(root, 0700); err != nil {
		_ = cleanup()
		return AgentAssets{}, err
	}
	if err := extractImage(image, root); err != nil {
		_ = cleanup()
		return AgentAssets{}, fmt.Errorf("prepare agent rootfs: %w", err)
	}
	source := filepath.Join(bundle, "wolfcow-guest")
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 32<<20 {
		_ = cleanup()
		return AgentAssets{}, fmt.Errorf("guest agent missing from runtime; run task build")
	}
	in, err := os.Open(source)
	if err != nil {
		_ = cleanup()
		return AgentAssets{}, err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(root, "wolfcow-guest"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		_ = cleanup()
		return AgentAssets{}, err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = cleanup()
		if copyErr != nil {
			return AgentAssets{}, copyErr
		}
		return AgentAssets{}, closeErr
	}
	return AgentAssets{Rootfs: root, Socket: filepath.Join(work, "control.sock"), Cleanup: cleanup}, nil
}
