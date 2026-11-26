// SPDX-License-Identifier: BSD-2-Clause

// demo runs one step of the demo on the host. Taskfile.yml runs the steps in
// order and skips the ones whose results are already there; this program
// does a step whenever it is asked to. Its environment:
//
//	WORK             where everything is built and kept (default: ./work)
//	EVIDENCE         where what the guest printed is kept (default: ./evidence)
//	MEMORY_MB, CPUS  the guest's size (default: 4096, 4)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

type demo struct {
	release    string // the unpacked release tree versions.yaml pins (release.go)
	machineCmd string // its bin/spin-machine
	memoryMB   int
	cpus       int

	here     string // this checkout
	work     string
	evidence string
	build    string // shirudo's checkout
	registry string // upatch-registry's checkout
	keyDir   string
	out      string
	run      string // the running machine's sockets, pid and logs
	pins     pins
	name     string // the running boot: provision or shirudo
}

type step struct {
	name string
	do   func(*demo) error
}

var steps = []step{
	{"release", (*demo).fetchRelease},
	{"pins", (*demo).pinsAgree},
	{"check", (*demo).check},
	{"shirudo", (*demo).shirudo},
	{"keys", (*demo).keys},
	{"images", (*demo).images},
	{"initrd", (*demo).initrd},
	{"packages", (*demo).packages},
	{"stage", (*demo).stage},
	{"provision", (*demo).provision},
	{"boot", (*demo).boot},
	{"init", (*demo).init},
	{"cve", (*demo).cve},
	{"obs", (*demo).obs},
	{"stop", (*demo).stop},
	{"running", (*demo).running},
}

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	i := slices.IndexFunc(steps, func(s step) bool { return s.name == os.Args[1] })
	if i < 0 {
		usage()
	}
	d, err := load()
	if err == nil {
		err = steps[i].do(d)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.name
	}
	fmt.Fprintf(os.Stderr, "usage: demo %v\n", names)
	os.Exit(2)
}

func load() (*demo, error) {
	here, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	abs := func(env, def string) (string, error) {
		v := os.Getenv(env)
		if v == "" {
			v = def
		}
		return filepath.Abs(v)
	}
	num := func(env string, def int) (int, error) {
		v := os.Getenv(env)
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s=%q is not a positive number", env, v)
		}
		return n, nil
	}
	d := &demo{here: here}
	if d.pins, err = loadPins(here); err != nil {
		return nil, err
	}
	if d.work, err = abs("WORK", "work"); err != nil {
		return nil, err
	}
	if d.evidence, err = abs("EVIDENCE", "evidence"); err != nil {
		return nil, err
	}
	if d.memoryMB, err = num("MEMORY_MB", 4096); err != nil {
		return nil, err
	}
	if d.cpus, err = num("CPUS", 4); err != nil {
		return nil, err
	}
	d.release = filepath.Join(d.work, "machine", releaseName(d.pins.machine), "usr/share/spin-stack")
	d.machineCmd = filepath.Join(d.release, "bin/spin-machine")
	d.build = filepath.Join(d.work, "src/shirudo")
	d.registry = filepath.Join(d.work, "src/upatch-registry")
	d.keyDir = filepath.Join(d.work, "keys")
	d.out = filepath.Join(d.work, "out")
	d.run = filepath.Join(d.work, "run")
	for _, dir := range []string{d.work, d.out, d.run, d.evidence, filepath.Join(d.work, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// Docker's and mkfs's temporary files go to the work disk, not a /tmp
	// that may be RAM.
	if os.Getenv("TMPDIR") == "" {
		os.Setenv("TMPDIR", filepath.Join(d.work, "tmp"))
	}
	return d, nil
}

func say(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "\033[1m==> "+format+"\033[0m\n", a...)
}
