package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	t.Setenv("WOLFCOW_ROOTFS", "")
	for _, tc := range []struct {
		args  []string
		want  string
		fails bool
	}{
		{nil, "Available Commands:", false},
		{[]string{"version"}, "0.0.1\n", false},

		{[]string{"shell", "/", "extra"}, "accepts at most 1 arg(s)", true},
		{[]string{"unknown"}, "unknown command", true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out bytes.Buffer
			err := Run(tc.args, &out)
			if (err != nil) != tc.fails {
				t.Fatalf("error = %v", err)
			}
			got := out.String()
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDefaultShell(t *testing.T) {
	t.Setenv("WOLFCOW_ROOTFS", "")
	called := false
	cmd := newCommand(func(path string) error {
		called = true
		if path != "" {
			t.Fatalf("default path = %q", path)
		}
		return nil
	})
	cmd.SetArgs([]string{"shell"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("launcher not called")
	}
}

func TestRootfsSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  string
		fails bool
	}{
		{"environment", []string{"shell"}, "/env-root", false},
		{"flag overrides environment", []string{"shell", "--rootfs", "/flag-root"}, "/flag-root", false},
		{"argument overrides environment", []string{"shell", "/arg-root"}, "/arg-root", false},
		{"ambiguous input", []string{"shell", "/arg-root", "--rootfs", "/flag-root"}, "", true},
		{"unknown flag", []string{"shell", "--typo"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WOLFCOW_ROOTFS", "/env-root")
			var got string
			cmd := newCommand(func(path string) error { got = path; return nil })
			cmd.SetArgs(tc.args)
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			err := cmd.Execute()
			if (err != nil) != tc.fails {
				t.Fatalf("error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("launched %q, want %q", got, tc.want)
			}
		})
	}
}
