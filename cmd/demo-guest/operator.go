// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	opRelease = stage + "/spin-stack"
	opOut     = stage + "/out"
	opShare   = "/usr/local/share/shirudo"
)

func operator() error {
	// The base image's root has ~100 MiB free: scratch, dracut included, goes on the disk.
	tmp := filepath.Join(stage, "tmp")
	keys := filepath.Join(opOut, "keys")
	images := filepath.Join(opOut, "images")
	packages := filepath.Join(opOut, "packages")
	configRoot := filepath.Join(tmp, "config-root")
	for _, dir := range []string{tmp, keys, images, packages, filepath.Join(configRoot, "etc/shirudo")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.Chmod(keys, 0o700); err != nil {
		return err
	}
	os.Setenv("TMPDIR", tmp)
	guestShirudo := filepath.Join(opRelease, "guest/shirudo")
	cert := filepath.Join(keys, "cert.der")

	heading("shirudo-operator/install, the release's, offline")
	if err := run(nil, filepath.Join(opRelease, "shirudo-operator/install")); err != nil {
		return err
	}

	heading("shirudo genkey: this run's operator key")
	if err := run(nil, "shirudo", "genkey", "--keys-dir="+keys); err != nil {
		return err
	}
	if err := run(nil, "shirudo", "bpm", "key", "fingerprint", cert); err != nil {
		return err
	}

	heading("the signed config image and the dm-verity runtime image")
	if err := os.WriteFile(filepath.Join(configRoot, "etc/shirudo/shirudo.conf"), []byte("eventlog.max_bytes = 0\n"), 0o644); err != nil {
		return err
	}
	runtimeConf := filepath.Join(tmp, "runtime.conf")
	if err := os.WriteFile(runtimeConf, []byte(`config.image = /var/lib/shirudo/images/initial
config.verification_key = /run/shirudo/runtime/etc/shirudo/config-cert.der
config.pcr = 14
tpm.tcti = device:/dev/tpmrm0
`), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"shirudo-gen-image", "--source", configRoot, "--output", "initial", "--keys-dir", keys, "--kind", "config"},
		{"shirudo", "image", "verify", "initial", "--cert", cert},
		{"shirudo-gen-runtime", "--binary", guestShirudo, "--bootstrap-conf", runtimeConf,
			"--cert", cert, "--config-cert", cert, "--output", "runtime", "--keys-dir", keys,
			"--unit-output", filepath.Join(opOut, "runtime-service.conf")},
	} {
		if err := runIn(images, nil, args[0], args[1:]...); err != nil {
			return err
		}
	}
	rootHash, err := os.ReadFile(filepath.Join(images, "runtime.roothash"))
	if err != nil {
		return err
	}
	bootstrap := filepath.Join(opOut, "uki-bootstrap.conf")
	if err := os.WriteFile(bootstrap, fmt.Appendf(nil,
		"runtime.image = /var/lib/shirudo/images/runtime\nruntime.root_hash = %s\n", strings.TrimSpace(string(rootHash))), 0o600); err != nil {
		return err
	}

	heading("CVE-2025-24813's package, from the registry's tools/build")
	// tools/build wants a shirudo checkout; the operator half's headers dir stands in for it.
	if err := run([]string{"SHIRUDO=shirudo"}, filepath.Join(registry, "tools/build"),
		opShare, keys, "1.0.0", filepath.Join(registry, "upatches", cveID)); err != nil {
		return err
	}
	if err := install(filepath.Join(registry, "build/packages", cveID+"-1.0.0.bee"), filepath.Join(packages, cveID+"-1.0.0.bee"), 0o644); err != nil {
		return err
	}

	heading("kernel-obs's package, the release's, from shirudo bpm build")
	obsSrc := filepath.Join(tmp, "kernel-obs")
	if err := os.RemoveAll(obsSrc); err != nil {
		return err
	}
	if err := os.CopyFS(obsSrc, os.DirFS(filepath.Join(opShare, "upatches/kernel-obs"))); err != nil {
		return err
	}
	if err := runIn(packages, nil, "shirudo", "bpm", "build", obsSrc, "--version", "1.0.0", "--keys-dir", keys); err != nil {
		return err
	}

	heading("the units and the systemd initrd")
	for _, unit := range []string{"shirudo.service", "shirudo-initrd.service"} {
		b, err := os.ReadFile(filepath.Join(opShare, "systemd", unit+".in"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(opOut, unit), []byte(strings.ReplaceAll(string(b), "@bindir@", "/usr/bin")), 0o644); err != nil {
			return err
		}
	}
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return err
	}
	initrd := filepath.Join(opOut, "initrd.img")
	if err := run(nil, "shirudo-gen-initrd", "--kver", unix.ByteSliceToString(uts.Release[:]), "--no-kernel",
		"--binary", guestShirudo, "--binary-path", "/usr/bin/shirudo",
		"--cert", cert, "--bootstrap-conf", bootstrap,
		"--unit", filepath.Join(opOut, "shirudo-initrd.service"), "--output", initrd,
		"--", "--no-hostonly", "--no-hostonly-cmdline", "--zstd"); err != nil {
		return err
	}
	if err := run(nil, "shirudo-gen-initrd", "--check", initrd); err != nil {
		return err
	}

	heading("what the operator made")
	if err := run(nil, "find", opOut, "-type", "f", "-printf", "%M %10s %P\n"); err != nil {
		return err
	}
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	// The host made the disk's root its user's (mkfs -E root_owner): given to
	// them, the host's debugfs rdump can set the owner it reads back.
	var st unix.Stat_t
	if err := unix.Stat(stage, &st); err != nil {
		return err
	}
	if err := run(nil, "chown", "-R", fmt.Sprintf("%d:%d", st.Uid, st.Gid), opOut); err != nil {
		return err
	}
	unix.Sync()
	return nil
}

func runIn(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}
