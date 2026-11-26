// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func cmd(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func output(name string, args ...string) (string, error) {
	b, err := exec.Command(name, args...).Output()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, ee.Stderr)
	}
	return strings.TrimSpace(string(b)), err
}

func (d *demo) check() error {
	if err := d.fetchRelease(); err != nil {
		return err
	}
	if _, err := os.Stat(d.guestCmd); err != nil {
		return fmt.Errorf("no demo-guest beside demo: %w", err)
	}
	for _, f := range []string{"bin/spin-machine", "bin/swtpm", "bin/passt", "bin/qemu-img", "bin/mkfs.ext4", "bin/debugfs",
		"e2fsprogs/mke2fs.conf", "kernel/vmlinux", "image/rootfs.qcow2", "guest/shirudo", "shirudo-operator/install"} {
		if _, err := os.Stat(filepath.Join(d.release, f)); err != nil {
			return fmt.Errorf("%s has no %s: versions.yaml pins a release from before the TPM, the network, bin/spin-machine or shirudo", d.release, f)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed")
	}
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("/dev/kvm is not usable: %w", err)
	}
	kvm.Close()
	if err := d.passtSandboxes(); err != nil {
		return err
	}
	say("release %s", d.release)
	return nil
}

func clone(repo, ref, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := cmd("", "git", "init", "-q", dir); err != nil {
			return err
		}
		if err := cmd(dir, "git", "remote", "add", "origin", repo); err != nil {
			return err
		}
	}
	if head, _ := output("git", "-C", dir, "rev-parse", "-q", "--verify", "HEAD"); head == ref {
		return nil
	}
	if err := cmd(dir, "git", "fetch", "-q", "--depth", "1", "origin", ref); err != nil {
		return err
	}
	return cmd(dir, "git", "checkout", "-q", "--detach", "FETCH_HEAD")
}

func (d *demo) opDisk() error {
	if err := clone(d.pins.registry.Source, d.pins.registry.Pin, d.registry); err != nil {
		return err
	}
	st := filepath.Join(d.work, "op")
	if err := os.RemoveAll(st); err != nil {
		return err
	}
	for _, dir := range []string{"spin-stack/guest", "registry", "bin"} {
		if err := os.MkdirAll(filepath.Join(st, dir), 0o755); err != nil {
			return err
		}
	}
	if err := cmd("", "cp", "-al", filepath.Join(d.release, "shirudo-operator"), filepath.Join(st, "spin-stack")); err != nil {
		return err
	}
	for from, to := range map[string]string{
		filepath.Join(d.release, "guest/shirudo"): "spin-stack/guest/shirudo",
		d.guestCmd: "bin/demo-guest",
	} {
		if err := copyFile(from, filepath.Join(st, to)); err != nil {
			return err
		}
	}
	if err := cmd("", "sh", "-c", `git -C "$1" archive HEAD | tar -x -C "$2"`, "sh", d.registry, filepath.Join(st, "registry")); err != nil {
		return err
	}
	return d.mkfs(st, d.opImg(), "op", "4G")
}

func (d *demo) opImg() string { return filepath.Join(d.work, "op.img") }

func (d *demo) mkfs(dir, img, label, size string) error {
	if err := os.Remove(img); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	owner := fmt.Sprintf("root_owner=%d:%d", os.Getuid(), os.Getgid())
	mkfs := exec.Command(filepath.Join(d.release, "bin/mkfs.ext4"), "-q", "-t", "ext4", "-L", label, "-E", owner, "-d", dir, img, size)
	mkfs.Env = append(os.Environ(), "MKE2FS_CONFIG="+filepath.Join(d.release, "e2fsprogs/mke2fs.conf"))
	mkfs.Stdout, mkfs.Stderr = os.Stderr, os.Stderr
	return mkfs.Run()
}

func (d *demo) opResults() error {
	if err := os.RemoveAll(d.out); err != nil {
		return err
	}
	if err := cmd("", filepath.Join(d.release, "bin/debugfs"), "-R", "rdump /out "+d.work, d.opImg()); err != nil {
		return err
	}
	for _, f := range []string{"keys/cert.der", "images/runtime.roothash", "packages/" + cvePackage, "packages/" + obsPackage,
		"shirudo.service", "runtime-service.conf", "initrd.img"} {
		if _, err := os.Stat(filepath.Join(d.out, f)); err != nil {
			return fmt.Errorf("the operator's guest left no %s", f)
		}
	}
	return nil
}

const (
	cvePackage = "cve-2025-24813-1.0.0.bee"
	obsPackage = "kernel-obs-1.0.0.bee"
)

func (d *demo) stage() error {
	say("staging what the guest gets")
	st := filepath.Join(d.out, "stage")
	if err := os.RemoveAll(st); err != nil {
		return err
	}
	for _, dir := range []string{"registry", "bin"} {
		if err := os.MkdirAll(filepath.Join(st, dir), 0o755); err != nil {
			return err
		}
	}
	for from, to := range map[string]string{
		filepath.Join(d.out, "images"):        "images",
		filepath.Join(d.out, "packages"):      "packages",
		filepath.Join(d.registry, "payloads"): "registry/payloads",
		filepath.Join(d.registry, "tools"):    "registry/tools",
	} {
		if err := os.CopyFS(filepath.Join(st, to), os.DirFS(from)); err != nil {
			return err
		}
	}
	for from, to := range map[string]string{
		filepath.Join(d.keyDir, "cert.der"):          "cert.der",
		filepath.Join(d.release, "guest/shirudo"):    "shirudo",
		filepath.Join(d.out, "runtime-service.conf"): "runtime-service.conf",
		filepath.Join(d.out, "shirudo.service"):      "shirudo.service",
		d.guestCmd:                                   "bin/demo-guest",
	} {
		if err := copyFile(from, filepath.Join(st, to)); err != nil {
			return err
		}
	}
	return d.mkfs(st, filepath.Join(d.out, "stage.img"), "stage", "512M")
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	fi, err := os.Stat(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, fi.Mode().Perm())
}

// passt exits 0 once its sandbox is up and it reads EOF on the closed peer.
func (d *demo) passtSandboxes() error {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	end := os.NewFile(uintptr(fds[1]), "passt")
	defer end.Close()
	if err := syscall.Close(fds[0]); err != nil {
		return err
	}
	passt := exec.Command(filepath.Join(d.release, "bin/passt"), "--foreground", "--fd", "3", "--ipv4-only")
	passt.ExtraFiles = []*os.File{end}
	said, err := passt.CombinedOutput()
	if err == nil {
		return nil
	}
	var host []string
	for _, k := range []string{"kernel/apparmor_restrict_unprivileged_userns", "kernel/apparmor_restrict_unprivileged_unconfined", "user/max_user_namespaces"} {
		v, _ := os.ReadFile("/proc/sys/" + k)
		host = append(host, k+"="+strings.TrimSpace(string(v)))
	}
	return fmt.Errorf(`passt cannot make its user namespace on this host, so boot --net would give the guest no network: %w
passt said: %s
this host: %s
Where AppArmor restricts unprivileged user namespaces, install passt's own profile once, as root
(spin-stack/machine docs/machine.md, "A network without root"):

  %s apparmor --release %s | sudo tee /etc/apparmor.d/spin-machine-passt >/dev/null
  sudo apparmor_parser -r /etc/apparmor.d/spin-machine-passt

It applies to this release's passt at this path alone; 'sudo apparmor_parser -R /etc/apparmor.d/spin-machine-passt'
and removing the file take it away`,
		err, strings.TrimSpace(string(said)), strings.Join(host, " "), d.machineCmd, d.release)
}
