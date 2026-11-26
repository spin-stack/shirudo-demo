// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"strings"
	"testing"

	"github.com/spin-stack/go-tools/versions"
)

func TestShirudoIsHeldToTheReleasesPin(t *testing.T) {
	const ours, other = "5795f8d4a7509af2afeb331046f179300a1756a2", "2a560729814a9ac0ac702bb15d39c2145522c143"
	release := func(pin string) []byte {
		return []byte("- name: shirudo\n  kind: git\n  source: https://github.com/shirudo-dev/shirudo.git\n  version: main\n  pin: " + pin + "\n  track: branch\n")
	}
	shirudo := versions.Entry{Name: "shirudo", Kind: versions.Git, Version: "main", Pin: ours, Track: "with spin-stack/machine@release shirudo pin"}
	if err := shirudoAgrees(shirudo, release(ours)); err != nil {
		t.Errorf("the release's own pin: %v", err)
	}
	err := shirudoAgrees(shirudo, release(other))
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Errorf("another pin: %v, want an error naming %s", err, other)
	}
}
