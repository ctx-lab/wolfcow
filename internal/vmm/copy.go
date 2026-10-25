package vmm

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// copyRootfs stages an optional host rootfs. The VM never receives its source path.
func copyRootfs(source, dest string) error {
	type link struct{ name, target string }
	var links []link
	var count int
	var total int64
	err := filepath.WalkDir(source, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == source {
			return nil
		}
		count++
		if count > 20000 {
			return fmt.Errorf("rootfs exceeds file limit")
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, err := safeName(rel); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			if info.Size() > 128<<20 {
				return fmt.Errorf("rootfs file too large: %s", rel)
			}
			total += info.Size()
			if total > 256<<20 {
				return fmt.Errorf("rootfs exceeds size limit")
			}
			fd, err := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if err != nil {
				return err
			}
			in := os.NewFile(uintptr(fd), name)
			defer in.Close()
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, in, info.Size())
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		case info.Mode()&os.ModeSymlink != 0:
			value, err := os.Readlink(name)
			if err != nil {
				return err
			}
			var resolved string
			if path.IsAbs(value) {
				resolved = path.Clean(strings.TrimPrefix(value, "/"))
			} else {
				resolved = path.Join(path.Dir(rel), value)
			}
			resolved, err = safeName(resolved)
			if err != nil || value == "" {
				return fmt.Errorf("unsafe rootfs symlink: %s", rel)
			}
			local, err := filepath.Rel(filepath.Dir(rel), resolved)
			if err != nil {
				return err
			}
			links = append(links, link{rel, local})
			return nil
		default:
			return fmt.Errorf("unsupported rootfs file: %s", rel)
		}
	})
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := os.Symlink(l.target, filepath.Join(dest, filepath.FromSlash(l.name))); err != nil {
			return err
		}
	}
	return nil
}
