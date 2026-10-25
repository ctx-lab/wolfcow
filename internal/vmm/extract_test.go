package vmm

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsArchiveEscapes(t *testing.T) {
	cases := []struct {
		name    string
		headers []*tar.Header
	}{
		{"parent path", []*tar.Header{{Name: "../outside", Typeflag: tar.TypeReg, Size: 1}}},
		{"absolute path", []*tar.Header{{Name: "/outside", Typeflag: tar.TypeReg, Size: 1}}},
		{"link escape", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../outside"}}},
		{"link parent", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}, {Name: "link/file", Typeflag: tar.TypeReg, Size: 1}}},
		{"device", []*tar.Header{{Name: "device", Typeflag: tar.TypeChar}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			for _, h := range tc.headers {
				if err := tw.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					if _, err := tw.Write([]byte("x")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := extractTar(tar.NewReader(&buf), root); err == nil {
				t.Fatal("accepted unsafe archive")
			}
			if _, err := os.Stat(filepath.Join(parent, "outside")); !os.IsNotExist(err) {
				t.Fatalf("outside path touched: %v", err)
			}
		})
	}
}

func TestExtractAbsoluteGuestLink(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []*tar.Header{
		{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "bin/sh", Typeflag: tar.TypeReg, Mode: 0755, Size: 2},
		{Name: "usr/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "usr/sh", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte("sh")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTar(tar.NewReader(&buf), root); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(root, "usr/sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(link, "/") || link != "../bin/sh" {
		t.Fatalf("link = %q", link)
	}
}
