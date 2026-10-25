package vmm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyRootfsDoesNotShareSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	dest := filepath.Join(t.TempDir(), "dest")
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(source, "bin/sh")
	if err := os.WriteFile(original, []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(source, "shell")); err != nil {
		t.Fatal(err)
	}
	if err := copyRootfs(source, dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "bin/sh"), []byte("changed"), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("source changed: %q", data)
	}
	link, err := os.Readlink(filepath.Join(dest, "shell"))
	if err != nil {
		t.Fatal(err)
	}
	if link != "bin/sh" {
		t.Fatalf("link = %q", link)
	}
}

func TestCopyRootfsRejectsEscapingLink(t *testing.T) {
	source := t.TempDir()
	dest := t.TempDir()
	if err := os.Symlink("../outside", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := copyRootfs(source, dest); err == nil {
		t.Fatal("accepted escaping link")
	}
}
