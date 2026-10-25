package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/ctx-lab/wolfcow/internal/cli"
)

func main() {
	if err := cli.RunWithWriters(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				code = 1
			}
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "wolfcow:", err)
		os.Exit(1)
	}
}
