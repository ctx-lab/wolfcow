package vmm

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extract only bounded directories, regular files, and confined symbolic links.
// Links are created last, so no file write can traverse an archive's symlink.
func extractImage(image, root string) error {
	f, err := os.Open(image)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractTar(tar.NewReader(gz), root)
}

func safeName(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	if path.IsAbs(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("unsafe image path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe image path %q", name)
		}
	}
	return path.Clean(name), nil
}

func extractTar(tr *tar.Reader, root string) error {
	type link struct{ name, target string }
	var links []link
	var total int64
	seen := map[string]bool{}
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if count >= 20000 || h.Size < 0 || h.Size > 128<<20 {
			return fmt.Errorf("Linux image exceeds extraction limits")
		}
		total += h.Size
		if total > 256<<20 {
			return fmt.Errorf("Linux image exceeds extraction limits")
		}
		name, err := safeName(h.Name)
		if err != nil {
			return err
		}
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if name == "." || seen[name] {
			return fmt.Errorf("duplicate or invalid image path %q", name)
		}
		seen[name] = true
		dest := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(h.Mode)&0777); err != nil {
				return err
			}
		case tar.TypeReg:
			out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, h.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			// Absolute guest links become equivalent relative links on the host.
			target := h.Linkname
			var resolved string
			if path.IsAbs(target) {
				resolved = path.Clean(strings.TrimPrefix(target, "/"))
			} else {
				resolved = path.Join(path.Dir(name), target)
			}
			resolved, err = safeName(resolved)
			if err != nil || target == "" {
				return fmt.Errorf("unsafe image symlink %q", name)
			}
			rel, err := filepath.Rel(filepath.Dir(name), resolved)
			if err != nil {
				return err
			}
			links = append(links, link{name, rel})
		default:
			return fmt.Errorf("unsupported image file type at %q", name)
		}
	}
	for _, l := range links {
		// A symlink cannot be a parent of any entry, including another link.
		prefix := l.name + "/"
		for name := range seen {
			if strings.HasPrefix(name, prefix) {
				return fmt.Errorf("image symlink is a parent: %q", l.name)
			}
		}
	}
	for _, l := range links {
		if err := os.Symlink(l.target, filepath.Join(root, filepath.FromSlash(l.name))); err != nil {
			return err
		}
	}
	return nil
}
