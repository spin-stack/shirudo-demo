// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"

	"github.com/spin-stack/go-tools/versions"
	shirudodemo "github.com/spin-stack/shirudo-demo"
)

// pins is what the demo is built from, versions.yaml's entries: each a
// commit or a digest, never a branch.
type pins struct {
	machine, shirudo, registry, ubuntu versions.Entry
}

func loadPins() (pins, error) {
	v, err := versions.Parse(shirudodemo.Versions)
	if err != nil {
		return pins{}, err
	}
	var p pins
	for name, e := range map[string]*versions.Entry{"spin-machine": &p.machine, "shirudo": &p.shirudo, "upatch-registry": &p.registry, "ubuntu": &p.ubuntu} {
		if *e, err = v.Get(name); err != nil {
			return pins{}, fmt.Errorf("versions.yaml: %w", err)
		}
	}
	return p, nil
}
