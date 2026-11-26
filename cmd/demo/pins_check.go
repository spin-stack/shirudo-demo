// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bytes"
	"fmt"

	"github.com/spin-stack/go-tools/versions"
)

// machineVersions is the versions.yaml of spin-stack/machine at a tag.
func machineVersions(tag string) string {
	return "https://raw.githubusercontent.com/spin-stack/machine/" + tag + "/versions.yaml"
}

// pinsAgree fails unless the shirudo the demo builds is the one the pinned
// release carries its kernel hooks from: its versions.yaml at the release's
// tag says which.
func (d *demo) pinsAgree() error {
	var raw bytes.Buffer
	url := machineVersions(d.pins.machine.Version)
	if err := get(url, &raw); err != nil {
		return err
	}
	if err := shirudoAgrees(d.pins.shirudo, raw.Bytes()); err != nil {
		return fmt.Errorf("%w (%s)", err, url)
	}
	say("shirudo %s: the commit spin-machine %s carries its hooks from", d.pins.shirudo.Pin, d.pins.machine.Version)
	return nil
}

// shirudoAgrees holds shirudo's pin to the one machine's versions.yaml,
// release, names for it.
func shirudoAgrees(shirudo versions.Entry, release []byte) error {
	file, err := versions.Parse(release)
	if err != nil {
		return err
	}
	want, err := versions.ExpectedIn(shirudo, file)
	if err != nil {
		return err
	}
	if shirudo.Pin != want {
		return fmt.Errorf("versions.yaml pins shirudo %s, and the spin-machine release carries its hooks from %s", shirudo.Pin, want)
	}
	return nil
}
