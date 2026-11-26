// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"path/filepath"

	"github.com/spin-stack/go-tools/versions"
)

// pins is what the demo is built from, versions.yaml's entries: each a
// commit or a digest, never a branch.
type pins struct {
	shirudo, registry, ubuntu versions.Entry
}

func loadPins(here string) (pins, error) {
	v, err := versions.Load(filepath.Join(here, "versions.yaml"))
	if err != nil {
		return pins{}, err
	}
	var p pins
	for name, e := range map[string]*versions.Entry{"shirudo": &p.shirudo, "upatch-registry": &p.registry, "ubuntu": &p.ubuntu} {
		if *e, err = v.Get(name); err != nil {
			return pins{}, fmt.Errorf("versions.yaml: %w", err)
		}
	}
	return p, nil
}
