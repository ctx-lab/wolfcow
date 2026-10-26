// Package network owns the temporary host process used for virtio-net probes.
package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Proxy struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
	stop    sync.Once
	log     string
}

// StartGVProxy starts Wolfcow's deny-by-default build of gvproxy on a private
// Unix datagram socket. Its guest-facing gateway exposes a health check and a
// static synthetic endpoint.
func StartGVProxy(ctx context.Context, bundle, socket string, policy Policy) (*Proxy, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(socket) {
		return nil, fmt.Errorf("gvproxy socket must be absolute")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return nil, fmt.Errorf("gvproxy socket already exists")
		}
		return nil, err
	}
	policyPath := filepath.Join(filepath.Dir(socket), "network-policy.json")
	policyFile, err := os.OpenFile(policyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(policyFile).Encode(policy); err != nil {
		_ = policyFile.Close()
		_ = os.Remove(policyPath)
		return nil, err
	}
	if err := policyFile.Close(); err != nil {
		_ = os.Remove(policyPath)
		return nil, err
	}
	logPath := filepath.Join(filepath.Dir(socket), "gvproxy.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.Remove(policyPath)
		return nil, err
	}
	cmd := exec.Command(filepath.Join(bundle, "gvproxy"), "--ssh-port", "-1", "--listen-vfkit", "unixgram:"+socket)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "WOLFCOW_POLICY_FILE=" + policyPath}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		_ = os.Remove(policyPath)
		return nil, fmt.Errorf("start gvproxy: %w", err)
	}
	_ = logFile.Close()
	p := &Proxy{cmd: cmd, done: make(chan struct{}), log: logPath}
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		info, err := os.Lstat(socket)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return p, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = p.Stop()
			return nil, err
		}
		select {
		case <-p.done:
			return nil, fmt.Errorf("gvproxy exited before ready: %v: %s", p.waitErr, p.logTail())
		case <-readyCtx.Done():
			_ = p.Stop()
			return nil, fmt.Errorf("gvproxy socket not ready: %w: %s", readyCtx.Err(), p.logTail())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (p *Proxy) Stop() error {
	if p == nil {
		return nil
	}
	p.stop.Do(func() { _ = p.cmd.Process.Signal(syscall.SIGTERM) })
	select {
	case <-p.done:
		return nil
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
			return nil
		case <-time.After(time.Second):
			return fmt.Errorf("gvproxy did not stop")
		}
	}
}

func (p *Proxy) logTail() string {
	data, err := os.ReadFile(p.log)
	if err != nil {
		return err.Error()
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	return string(data)
}

// LogTail returns bounded diagnostic output from the temporary proxy process.
func (p *Proxy) LogTail() string {
	if p == nil {
		return ""
	}
	return p.logTail()
}
