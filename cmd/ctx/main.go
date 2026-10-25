package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/ctx-lab/ctx/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				code = 1
			}
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "ctx:", err)
		os.Exit(1)
	}
}
