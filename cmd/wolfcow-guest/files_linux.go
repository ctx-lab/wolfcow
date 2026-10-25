//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/ctx-lab/wolfcow/internal/protocol"
	"github.com/ctx-lab/wolfcow/internal/workspace"
)

func handleFile(conn *os.File, request protocol.Message) {
	respond := func(m protocol.Message) { m.ID = request.ID; _ = protocol.Write(conn, m) }
	fail := func(err error) { respond(protocol.Message{Type: "error", Code: "BAD_REQUEST", Error: err.Error()}) }
	switch request.Type {
	case "file.read":
		if err := workspace.SafePath(request.Path); err != nil {
			fail(err)
			return
		}
		files, err := workspace.ReadSnapshot("/work")
		if err != nil {
			fail(err)
			return
		}
		data, ok := files[request.Path]
		if !ok {
			fail(fmt.Errorf("file not found: %s", request.Path))
			return
		}
		respond(protocol.Message{Type: "file.data", Path: request.Path, Data: data})
	case "file.write":
		if err := workspace.SafePath(request.Path); err != nil {
			fail(err)
			return
		}
		if len(request.Data) > protocol.MaxFile {
			fail(fmt.Errorf("file exceeds size limit"))
			return
		}
		// Check the existing tree before writing; links and special files are rejected.
		files, err := workspace.ReadSnapshot("/work")
		if err != nil {
			fail(err)
			return
		}
		var total int
		for name, data := range files {
			if name != request.Path {
				total += len(data)
			}
		}
		_, exists := files[request.Path]
		if total+len(request.Data) > protocol.MaxWorkspace || len(files) >= protocol.MaxFiles && !exists {
			fail(fmt.Errorf("workspace limit exceeded"))
			return
		}
		target := filepath.Join("/work", filepath.FromSlash(request.Path))
		parent := "/work"
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(request.Path)), "/") {
			if part == "." {
				continue
			}
			parent = filepath.Join(parent, part)
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() {
				fail(fmt.Errorf("parent is not a workspace directory"))
				return
			}
		}
		fd, err := syscall.Open(target, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			fail(err)
			return
		}
		f := os.NewFile(uintptr(fd), target)
		_, err = f.Write(request.Data)
		closeErr := f.Close()
		if err != nil {
			fail(err)
			return
		}
		if closeErr != nil {
			fail(closeErr)
			return
		}
		respond(protocol.Message{Type: "file.written", Path: request.Path})
	case "workspace.diff":
		after, err := workspace.ReadSnapshot("/work")
		if err != nil {
			fail(err)
			return
		}
		paths := make([]string, 0, len(after))
		for name := range after {
			paths = append(paths, name)
		}
		sort.Strings(paths)
		respond(protocol.Message{Type: "workspace.files", Files: paths})
	}
}
