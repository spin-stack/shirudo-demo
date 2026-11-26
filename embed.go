// SPDX-License-Identifier: BSD-2-Clause

// Package shirudodemo holds the files cmd/demo reads as it runs, compiled into
// it, so that a released demo needs no checkout beside it.
package shirudodemo

import _ "embed"

// Versions is versions.yaml: what the demo is built from.
//
//go:embed versions.yaml
var Versions []byte

// InitrdDockerfile is the image shirudo's initrd is made in (README, "How the
// guest differs"). It copies nothing in, so it is built with no context.
//
//go:embed initrd/Dockerfile
var InitrdDockerfile []byte
