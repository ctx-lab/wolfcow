package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctx-lab/wolfcow/internal/protocol"
)

func TestSafePath(t *testing.T) {
	for _, name := range []string{"", ".", "../escape", "a/../escape", "/absolute", "a//b", "a\\b", "a/./b", "line\nbreak"} {
		if err := SafePath(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"note.txt", "dir/file.txt"} {
		if err := SafePath(name); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
}

func TestSnapshotRejectsLinksAndLimits(t *testing.T) {
	root := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(linkRoot); err == nil {
		t.Fatal("accepted linked root")
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(root); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large"), make([]byte, protocol.MaxFile+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(root); err == nil {
		t.Fatal("accepted oversized file")
	}
}

func TestCopyChangesAndDiff(t *testing.T) {
	before := Snapshot{"old.txt": []byte("old\n"), "gone.txt": []byte("gone\n")}
	root := filepath.Join(t.TempDir(), "copy")
	if err := WriteSnapshot(root, before); err != nil {
		t.Fatal(err)
	}
	copy, err := ReadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(copy["old.txt"]) != "old\n" {
		t.Fatalf("copy = %q", copy["old.txt"])
	}
	after := Snapshot{"old.txt": []byte("new\n"), "added.txt": []byte("added\n")}
	changes, err := Changes(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Kind != "created" || changes[1].Kind != "deleted" || changes[2].Kind != "modified" {
		t.Fatalf("changes = %+v", changes)
	}
	diff, err := Unified(before, after, changes)
	if err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{"--- /dev/null", "+++ b/added.txt", "-old", "+new"} {
		if !strings.Contains(diff, piece) {
			t.Fatalf("diff missing %q: %s", piece, diff)
		}
	}
}

func TestBinaryDiffHasMarker(t *testing.T) {
	diff, err := Unified(Snapshot{}, Snapshot{"binary": []byte{0}}, []protocol.Change{{Path: "binary", Kind: "created"}})
	if err != nil {
		t.Fatal(err)
	}
	if diff != "Binary files /dev/null and b/binary differ\n" {
		t.Fatalf("diff = %q", diff)
	}
}

func TestUnifiedMarksMissingFinalNewline(t *testing.T) {
	old := Snapshot{"note": []byte("before")}
	newFiles := Snapshot{"note": []byte("after\n")}
	changes, err := Changes(old, newFiles)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := Unified(old, newFiles, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "-before\n\\ No newline at end of file\n+after\n") {
		t.Fatalf("diff = %q", diff)
	}
}
