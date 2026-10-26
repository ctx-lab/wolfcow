package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, entries []struct {
	name string
	body string
}) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "image.tar.gz")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestInspectRecordsPackagesAndFiles(t *testing.T) {
	image := fixture(t, []struct {
		name string
		body string
	}{
		{"./note.txt", "hello\n"},
		{"./lib/apk/db/installed", "P:busybox\nV:1.2-r0\nL:GPL-2.0-only\n\n"},
	})
	m, err := inspect(image, "https://example.invalid/image", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	if m.ArchiveSHA256 == "" || len(m.Packages) != 1 || m.Packages[0].Name != "busybox" || len(m.Entries) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	if m.Entries[0].Path != "lib/apk/db/installed" || m.Entries[1].Path != "note.txt" || m.Entries[1].SHA256 == "" {
		t.Fatalf("entries = %+v", m.Entries)
	}
}

func TestInspectRejectsPathEscape(t *testing.T) {
	image := fixture(t, []struct {
		name string
		body string
	}{{"../escape", "bad"}})
	if _, err := inspect(image, "source", "version"); err == nil {
		t.Fatal("accepted path escape")
	}
}
