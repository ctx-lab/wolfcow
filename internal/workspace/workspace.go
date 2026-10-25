// Package workspace handles bounded, regular-file-only copies and changes.
package workspace

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/ctx-lab/wolfcow/internal/protocol"
)

type Snapshot map[string][]byte

func SafePath(name string) error {
	if name == "" || name == "." || !utf8.ValidString(name) || path.IsAbs(name) || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return fmt.Errorf("unsafe workspace path %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("unsafe workspace path %q", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("unsafe workspace path %q", name)
		}
	}
	return nil
}

// ReadSnapshot rejects links and special files, and never follows an input symlink.
func ReadSnapshot(root string) (Snapshot, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory")
	}
	files := Snapshot{}
	var total int64
	err = filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := SafePath(rel); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported workspace entry %q", rel)
		}
		if len(files) >= protocol.MaxFiles {
			return fmt.Errorf("workspace exceeds file limit")
		}
		fd, err := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > protocol.MaxFile || total+info.Size() > protocol.MaxWorkspace {
			f.Close()
			return fmt.Errorf("workspace file or total size limit: %s", rel)
		}
		data, err := io.ReadAll(io.LimitReader(f, protocol.MaxFile+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > protocol.MaxFile {
			return fmt.Errorf("workspace file too large: %s", rel)
		}
		total += int64(len(data))
		if total > protocol.MaxWorkspace {
			return fmt.Errorf("workspace exceeds size limit")
		}
		files[rel] = data
		return nil
	})
	return files, err
}

func WriteSnapshot(root string, files Snapshot) error {
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	for name, data := range files {
		if err := SafePath(name); err != nil {
			return err
		}
		if len(data) > protocol.MaxFile {
			return fmt.Errorf("workspace file too large: %s", name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func Changes(before, after Snapshot) ([]protocol.Change, error) {
	set := map[string]bool{}
	for name := range before {
		set[name] = true
	}
	for name := range after {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	var changes []protocol.Change
	for _, name := range names {
		if err := SafePath(name); err != nil {
			return nil, err
		}
		old, existed := before[name]
		newData, exists := after[name]
		if existed && exists && bytes.Equal(old, newData) {
			continue
		}
		kind := "modified"
		if !existed {
			kind = "created"
		} else if !exists {
			kind = "deleted"
		}
		changes = append(changes, protocol.Change{Path: name, Kind: kind})
	}
	return changes, nil
}

// Unified emits one whole-file hunk per changed text file.
func Unified(before, after Snapshot, changes []protocol.Change) (string, error) {
	var b strings.Builder
	for _, change := range changes {
		old := before[change.Path]
		newData := after[change.Path]
		if !utf8.Valid(old) || !utf8.Valid(newData) || bytes.IndexByte(old, 0) >= 0 || bytes.IndexByte(newData, 0) >= 0 {
			return "", fmt.Errorf("binary diff unsupported: %s", change.Path)
		}
		oldName, newName := "a/"+change.Path, "b/"+change.Path
		if change.Kind == "created" {
			oldName = "/dev/null"
		}
		if change.Kind == "deleted" {
			newName = "/dev/null"
		}
		oldLines := splitLines(string(old))
		newLines := splitLines(string(newData))
		fmt.Fprintf(&b, "--- %s\n+++ %s\n@@ -%d,%d +%d,%d @@\n", oldName, newName, hunkStart(len(oldLines)), len(oldLines), hunkStart(len(newLines)), len(newLines))
		for _, line := range oldLines {
			writeDiffLine(&b, '-', line)
		}
		for _, line := range newLines {
			writeDiffLine(&b, '+', line)
		}
	}
	return b.String(), nil
}

func hunkStart(n int) int {
	if n == 0 {
		return 0
	}
	return 1
}
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeDiffLine(b *strings.Builder, sign byte, line string) {
	b.WriteByte(sign)
	b.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		b.WriteString("\n\\ No newline at end of file\n")
	}
}
