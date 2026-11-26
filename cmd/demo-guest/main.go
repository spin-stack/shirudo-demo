// SPDX-License-Identifier: BSD-2-Clause

// demo-guest is the demo's side inside the guest. It runs as root from the
// stage disk, mounted at /opt/demo, and prints what it does under "## "
// headings; the host keeps that output in evidence/.
//
//	demo-guest provision   place shirudo's images and units, before the first boot with enforcement
//	demo-guest init        what the machine booted into; fails unless shirudo status is ok
//	demo-guest cve         CVE-2025-24813: payload, load, payload, unload, payload; expects 0, 10, 0
//	demo-guest cve-test    the registry's own tools/test, as its README runs it
//	demo-guest obs         kernel-obs: make the kernel do five things and read them back
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
		"provision": provision,
		"init":      initStatus,
		"cve":       cve,
		"cve-test":  cveTest,
		"obs":       obs,
	}
	if len(os.Args) != 2 || steps[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: demo-guest provision|init|cve|cve-test|obs")
		os.Exit(2)
	}
	if err := steps[os.Args[1]](); err != nil {
		fmt.Fprintf(os.Stderr, "demo-guest %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func heading(format string, a ...any) { fmt.Printf("## "+format+"\n", a...) }

// out is where a command's output goes: a pipe to ours, not the console's
// terminal, so a program that draws progress on a terminal, as dpkg does, keeps
// it out of evidence/.
var out io.Writer = struct{ io.Writer }{os.Stdout}

// run runs a command with its output on ours.
func run(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}

// status is a command's exit status, or -1 when it did not run.
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
