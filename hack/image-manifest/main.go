// Command image-manifest records the exact contents of the pinned Linux rootfs.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

type entry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

type apkPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	License string `json:"license"`
}

type manifest struct {
	Schema        int          `json:"schema"`
	Source        string       `json:"source"`
	Version       string       `json:"version"`
	Architecture  string       `json:"architecture"`
	ArchiveSHA256 string       `json:"archive_sha256"`
	ArchiveBytes  int64        `json:"archive_bytes"`
	ContentBytes  int64        `json:"content_bytes"`
	Packages      []apkPackage `json:"packages"`
	Entries       []entry      `json:"entries"`
}

func main() {
	image := flag.String("image", "", "rootfs tar.gz path")
	source := flag.String("source", "", "pinned source URL")
	version := flag.String("version", "", "source image version")
	flag.Parse()
	if *image == "" || *source == "" || *version == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: image-manifest -image FILE -source URL -version VERSION")
		os.Exit(2)
	}
	m, err := inspect(*image, *source, *version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "image-manifest:", err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "image-manifest:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "image-manifest:", err)
		os.Exit(1)
	}
}

func inspect(image, source, version string) (manifest, error) {
	f, err := os.Open(image)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return manifest{}, fmt.Errorf("image archive exceeds 32 MiB")
	}
	archiveHash := sha256.New()
	if _, err := io.Copy(archiveHash, f); err != nil {
		return manifest{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return manifest{}, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return manifest{}, err
	}
	defer gz.Close()
	m := manifest{Schema: 1, Source: source, Version: version, Architecture: "aarch64", ArchiveSHA256: hex.EncodeToString(archiveHash.Sum(nil)), ArchiveBytes: info.Size(), Packages: []apkPackage{}, Entries: []entry{}}
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var apkDB bytes.Buffer
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest{}, err
		}
		name, err := safePath(h.Name)
		if err != nil {
			return manifest{}, err
		}
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if name == "." || seen[name] || len(m.Entries) >= 20000 || h.Size < 0 || h.Size > 128<<20 {
			return manifest{}, fmt.Errorf("invalid or oversized archive entry %q", h.Name)
		}
		seen[name] = true
		e := entry{Path: name, Mode: fmt.Sprintf("%04o", h.Mode&07777)}
		switch h.Typeflag {
		case tar.TypeDir:
			e.Type = "directory"
		case tar.TypeSymlink:
			e.Type = "symlink"
			e.Target = h.Linkname
		case tar.TypeReg:
			e.Type, e.Size = "file", h.Size
			m.ContentBytes += h.Size
			if m.ContentBytes > 256<<20 {
				return manifest{}, fmt.Errorf("image contents exceed 256 MiB")
			}
			hash := sha256.New()
			var writer io.Writer = hash
			if name == "lib/apk/db/installed" {
				if h.Size > 4<<20 {
					return manifest{}, fmt.Errorf("APK database too large")
				}
				writer = io.MultiWriter(hash, &apkDB)
			}
			if _, err := io.CopyN(writer, reader, h.Size); err != nil {
				return manifest{}, err
			}
			e.SHA256 = hex.EncodeToString(hash.Sum(nil))
		default:
			return manifest{}, fmt.Errorf("unsupported archive entry %q", h.Name)
		}
		m.Entries = append(m.Entries, e)
	}
	if apkDB.Len() == 0 {
		return manifest{}, fmt.Errorf("image has no APK package database")
	}
	m.Packages, err = parsePackages(apkDB.Bytes())
	if err != nil {
		return manifest{}, err
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m, nil
}

func safePath(name string) (string, error) {
	if name == "./" || name == "." {
		return ".", nil
	}
	name = strings.TrimPrefix(name, "./")
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("unsafe image path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe image path %q", name)
		}
	}
	return path.Clean(name), nil
}

func parsePackages(data []byte) ([]apkPackage, error) {
	var packages []apkPackage
	for _, block := range bytes.Split(data, []byte("\n\n")) {
		var p apkPackage
		for _, line := range bytes.Split(block, []byte("\n")) {
			s := string(line)
			switch {
			case strings.HasPrefix(s, "P:"):
				p.Name = strings.TrimPrefix(s, "P:")
			case strings.HasPrefix(s, "V:"):
				p.Version = strings.TrimPrefix(s, "V:")
			case strings.HasPrefix(s, "L:"):
				p.License = strings.TrimPrefix(s, "L:")
			}
		}
		if p.Name == "" {
			continue
		}
		if p.Version == "" || p.License == "" {
			return nil, fmt.Errorf("incomplete APK package %q", p.Name)
		}
		packages = append(packages, p)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("no APK packages found")
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}
