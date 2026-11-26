// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"

	"github.com/spin-stack/go-tools/versions"
	shirudodemo "github.com/spin-stack/shirudo-demo"
)

type pins struct {
	machine, operator, registry versions.Entry
}

func loadPins() (pins, error) {
	v, err := versions.Parse(shirudodemo.Versions)
	if err != nil {
		return pins{}, err
	}
	var p pins
	for name, e := range map[string]*versions.Entry{"spin-machine": &p.machine, "shirudo-operator": &p.operator, "upatch-registry": &p.registry} {
		if *e, err = v.Get(name); err != nil {
			return pins{}, fmt.Errorf("versions.yaml: %w", err)
		}
	}
	return p, nil
}
