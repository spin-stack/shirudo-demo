// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

const (
	stage   = "/opt/demo"
	work    = "/var/lib/shirudo-demo"
	shirudo = "/run/shirudo/runtime/usr/bin/shirudo"
)

func main() {
	steps := map[string]func() error{
		"operator":  operator,
		"provision": provision,
		"init":      initStatus,
		"cve":       cve,
		"cve-test":  cveTest,
		"obs":       obs,
	}
	if len(os.Args) != 2 || steps[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: demo-guest operator|provision|init|cve|cve-test|obs")
		os.Exit(2)
	}
	if err := steps[os.Args[1]](); err != nil {
		fmt.Fprintf(os.Stderr, "demo-guest %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func heading(format string, a ...any) { fmt.Printf("## "+format+"\n", a...) }

// Not an *os.File, so os/exec gives children a pipe instead of the console tty (keeps dpkg progress bars out).
var out io.Writer = struct{ io.Writer }{os.Stdout}

func run(env []string, name string, args ...string) error { return runIn("", env, name, args...) }

func status(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err)
	return -1
}
