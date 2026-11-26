// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

type demo struct {
	release    string
	machineCmd string
	guestCmd   string
	memoryMB   int
	cpus       int

	work     string
	evidence string
	registry string
	keyDir   string
	out      string
	run      string
	pins     pins
	name     string
}

type step struct {
	name   string
	do     func(*demo) error
	byName bool
}

var steps = []step{
	{name: "machine", do: (*demo).fetchRelease},
	{name: "check", do: (*demo).check},
	{name: "operator", do: (*demo).operator},
	{name: "stage", do: (*demo).stage},
	{name: "provision", do: (*demo).provision},
	{name: "boot", do: (*demo).boot},
	{name: "init", do: (*demo).init},
	{name: "cve", do: (*demo).cve},
	{name: "obs", do: (*demo).obs},
	{name: "stop", do: (*demo).stop},
	{name: "running", do: (*demo).running, byName: true},
}

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	todo := steps
	if os.Args[1] == "run" {
		todo = slices.DeleteFunc(slices.Clone(steps), func(s step) bool { return s.byName })
	} else if i := slices.IndexFunc(steps, func(s step) bool { return s.name == os.Args[1] }); i >= 0 {
		todo = steps[i : i+1]
	} else {
		usage()
	}
	d, err := load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo: %v\n", err)
		os.Exit(1)
	}
	for _, s := range todo {
		if err := s.do(d); err != nil {
			fmt.Fprintf(os.Stderr, "demo %s: %v\n", s.name, err)
			os.Exit(1)
		}
	}
}

func usage() {
	names := []string{"run"}
	for _, s := range steps {
		names = append(names, s.name)
	}
	fmt.Fprintf(os.Stderr, "usage: demo %v\n", names)
	os.Exit(2)
}

func load() (*demo, error) {
	self, err := os.Executable()
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
	d := &demo{guestCmd: filepath.Join(filepath.Dir(self), "demo-guest")}
	if d.pins, err = loadPins(); err != nil {
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
	d.registry = filepath.Join(d.work, "src/upatch-registry")
	d.out = filepath.Join(d.work, "out")
	d.keyDir = filepath.Join(d.out, "keys")
	d.run = filepath.Join(d.work, "run")
	for _, dir := range []string{d.work, d.out, d.run, d.evidence} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func say(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "\033[1m==> "+format+"\033[0m\n", a...)
}
