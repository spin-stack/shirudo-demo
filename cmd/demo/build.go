// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// cmd runs a command with its output on ours.
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, ee.Stderr)
	}
	return strings.TrimSpace(string(b)), err
}

func (d *demo) check() error {
	for _, f := range []string{"bin/spin-machine", "bin/swtpm", "bin/passt", "bin/qemu-img", "bin/mkfs.ext4", "e2fsprogs/mke2fs.conf", "kernel/vmlinux", "image/rootfs.qcow2"} {
		if _, err := os.Stat(filepath.Join(d.release, f)); err != nil {
			return fmt.Errorf("%s has no %s: versions.yaml pins a release from before the TPM, the network or bin/spin-machine", d.release, f)
		}
	}
	for _, tool := range []string{"docker", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is not installed", tool)
		}
	}
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("/dev/kvm is not usable: %w", err)
	}
	kvm.Close()
	if err := d.pinsAgree(); err != nil {
		return err
	}
	if err := d.passtSandboxes(); err != nil {
		return err
	}
	kver, err := d.kernelRelease()
	if err != nil {
		return err
	}
	say("release %s, kernel %s", d.release, kver)
	return nil
}

var linuxVersion = regexp.MustCompile(`Linux version (\S+) `)

func (d *demo) kernelRelease() (string, error) {
	b, err := os.ReadFile(filepath.Join(d.release, "kernel/vmlinux"))
	if err != nil {
		return "", err
	}
	m := linuxVersion.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s/kernel/vmlinux names no Linux version", d.release)
	}
	return string(m[1]), nil
}

// operator runs a command in shirudo's own build container
// (testing/container), which its checkout's run.sh builds once. Every path
// is mounted at itself, and the command runs in dir.
func (d *demo) operator(dir string, args ...string) error {
	runSh := filepath.Join(d.build, "testing/container/run.sh")
	image, err := output(runSh, "--image")
	if err != nil {
		return err
	}
	if exec.Command("docker", "image", "inspect", image).Run() != nil {
		c := exec.Command(runSh, "--", "true")
		c.Env = append(os.Environ(), "BUILDX_BUILDER="+cmp(os.Getenv("BUILDX_BUILDER"), "default"))
		c.Stdout, c.Stderr = os.Stderr, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("building shirudo's container: %w", err)
		}
	}
	path := strings.Join([]string{d.build, d.build + "/tools/shirudo-build", d.build + "/tools/gen-image",
		"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, ":")
	return cmd("", "docker", append([]string{"run", "--rm", "--init", "--user", ids(), "--ulimit", "memlock=-1:-1",
		"-e", "HOME=/tmp", "-e", "GIT_CONFIG_COUNT=1", "-e", "GIT_CONFIG_KEY_0=safe.directory", "-e", "GIT_CONFIG_VALUE_0=*",
		"-e", "PATH=" + path,
		"-v", d.work + ":" + d.work, "-v", d.release + ":" + d.release + ":ro", "-w", dir,
		image}, args...)...)
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func ids() string { return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()) }

// clone checks out one commit of a repository into dir, fetching only it.
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

// shirudo clones shirudo and the registry at their pins and builds shirudo
// against the release kernel's BTF.
func (d *demo) shirudo() error {
	if err := clone(d.pins.shirudo.Source, d.pins.shirudo.Pin, d.build); err != nil {
		return err
	}
	if err := clone(d.pins.registry.Source, d.pins.registry.Pin, d.registry); err != nil {
		return err
	}
	say("building shirudo %s in its container, against the release kernel's BTF", d.pins.shirudo.Pin)
	btf := "BPF_VMLINUX_BTF=" + filepath.Join(d.release, "kernel/vmlinux")
	jobs := "-j" + strconv.Itoa(runtime.NumCPU())
	if err := d.operator(d.build, "make", jobs, btf); err != nil {
		return err
	}
	if err := d.operator(d.build, "make", jobs, btf, "target", "shirudo-build"); err != nil {
		return err
	}
	return d.operator(d.build, filepath.Join(d.build, "shirudo-target"), "version")
}

// keys makes this run's ML-DSA-87 operator key. It is never committed, and
// a new run makes a new one.
func (d *demo) keys() error {
	say("making this run's operator key (never committed)")
	if err := os.MkdirAll(d.keyDir, 0o700); err != nil {
		return err
	}
	if err := d.operator(d.work, "shirudo", "genkey", "--keys-dir="+d.keyDir); err != nil {
		return err
	}
	return d.operator(d.work, "shirudo", "bpm", "key", "fingerprint", filepath.Join(d.keyDir, "cert.der"))
}

// images builds the signed config image and the dm-verity runtime image, as
// shirudo's boot integration guide builds them, and the initrd's bootstrap
// file that names the runtime image's root hash.
func (d *demo) images() error {
	say("building the signed config image and the dm-verity runtime image")
	img := filepath.Join(d.out, "images")
	configRoot := filepath.Join(d.out, "config-root")
	for _, dir := range []string{img, configRoot} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(img, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(configRoot, "etc/shirudo/shirudo.conf"), "eventlog.max_bytes = 0\n", 0o644); err != nil {
		return err
	}
	runtimeConf := filepath.Join(d.out, "runtime.conf")
	if err := writeFile(runtimeConf, `config.image = /var/lib/shirudo/images/initial
config.verification_key = /run/shirudo/runtime/etc/shirudo/config-cert.der
config.pcr = 14
tpm.tcti = device:/dev/tpmrm0
`, 0o644); err != nil {
		return err
	}
	cert := filepath.Join(d.keyDir, "cert.der")
	for _, args := range [][]string{
		{"shirudo-gen-image", "--source", configRoot, "--output", "initial", "--keys-dir", d.keyDir, "--kind", "config"},
		{"shirudo", "image", "verify", "initial", "--cert", cert},
		{"shirudo-gen-runtime", "--binary", filepath.Join(d.build, "shirudo-target"), "--bootstrap-conf", runtimeConf,
			"--cert", cert, "--config-cert", cert, "--output", "runtime", "--keys-dir", d.keyDir,
			"--unit-output", filepath.Join(d.out, "runtime-service.conf")},
	} {
		if err := d.operator(img, args...); err != nil {
			return err
		}
	}
	rootHash, err := os.ReadFile(filepath.Join(img, "runtime.roothash"))
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(d.out, "uki-bootstrap.conf"), fmt.Sprintf(
		"runtime.image = /var/lib/shirudo/images/runtime\nruntime.root_hash = %s\n", bytes.TrimSpace(rootHash)), 0o600)
}

// initrd builds the systemd initrd shirudo's init runs in: shirudo-gen-initrd
// runs dracut with shirudo's module in Ubuntu 26.04's systemd (initrd/
// Dockerfile, README), then checks the result.
func (d *demo) initrd() error {
	kver, err := d.kernelRelease()
	if err != nil {
		return err
	}
	say("building the shirudo initrd for %s with dracut, in Ubuntu 26.04's systemd", kver)
	if err := cmd("", "docker", "build", "-q", "--build-arg", "UBUNTU_IMAGE="+d.pins.ubuntu.Source+"@"+d.pins.ubuntu.Pin,
		"-t", "shirudo-demo-initrd", filepath.Join(d.here, "initrd")); err != nil {
		return err
	}
	unit := filepath.Join(d.out, "shirudo-initrd.service")
	if err := d.unit("shirudo-initrd.service", unit); err != nil {
		return err
	}
	genInitrd := filepath.Join(d.build, "tools/gen-initrd/shirudo-gen-initrd")
	initrd := filepath.Join(d.out, "initrd.img")
	docker := []string{"run", "--rm", "--user", ids(), "-e", "TMPDIR=/tmp", "-v", d.work + ":" + d.work, "shirudo-demo-initrd", genInitrd}
	if err := cmd("", "docker", append(docker,
		"--kver", kver, "--no-kernel",
		"--binary", filepath.Join(d.build, "shirudo-target"), "--binary-path", "/usr/bin/shirudo",
		"--cert", filepath.Join(d.keyDir, "cert.der"), "--bootstrap-conf", filepath.Join(d.out, "uki-bootstrap.conf"),
		"--unit", unit, "--module-dir", filepath.Join(d.build, "tools/gen-initrd"), "--output", initrd,
		"--", "--no-hostonly", "--no-hostonly-cmdline", "--zstd")...); err != nil {
		return err
	}
	return cmd("", "docker", append(docker, "--check", initrd)...)
}

// unit writes one of shirudo's systemd units, its @bindir@ filled in.
func (d *demo) unit(name, to string) error {
	b, err := os.ReadFile(filepath.Join(d.build, "systemd", name+".in"))
	if err != nil {
		return err
	}
	return writeFile(to, strings.ReplaceAll(string(b), "@bindir@", "/usr/bin"), 0o644)
}

// packages builds and signs CVE-2025-24813's package with the registry's
// tools/build, and shirudo's kernel-obs with shirudo bpm build.
func (d *demo) packages() error {
	pk := filepath.Join(d.out, "packages")
	if err := os.MkdirAll(pk, 0o755); err != nil {
		return err
	}
	say("building and signing CVE-2025-24813's upatch with the registry's tools/build")
	if err := d.operator(d.work, "env", "SHIRUDO="+filepath.Join(d.build, "shirudo"),
		filepath.Join(d.registry, "tools/build"), d.build, d.keyDir, "1.0.0",
		filepath.Join(d.registry, "upatches/cve-2025-24813")); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(d.registry, "build/packages/cve-2025-24813-1.0.0.bee"),
		filepath.Join(pk, "cve-2025-24813-1.0.0.bee")); err != nil {
		return err
	}

	say("building and signing shirudo's kernel-obs package")
	// Staged the way the registry's tools/build stages a package: a copy,
	// with shirudo's vendor/ and bpf/ beside its sources.
	src := filepath.Join(d.out, "kernel-obs")
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	if err := os.CopyFS(src, os.DirFS(filepath.Join(d.build, "upatches/kernel-obs"))); err != nil {
		return err
	}
	for _, dir := range []string{"vendor", "bpf"} {
		if err := os.Symlink(filepath.Join(d.build, dir), filepath.Join(src, dir)); err != nil {
			return err
		}
	}
	return d.operator(pk, "shirudo", "bpm", "build", src, "--version", "1.0.0", "--keys-dir", d.keyDir)
}

// stage makes the ext4 image the guest mounts at /opt/demo: shirudo's
// images, certificate, binary and units, the guest's own program, the
// packages and the registry's payloads and tools.
func (d *demo) stage() error {
	say("staging what the guest gets")
	st := filepath.Join(d.out, "stage")
	if err := os.RemoveAll(st); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(st, "registry"), 0o755); err != nil {
		return err
	}
	for from, to := range map[string]string{
		filepath.Join(d.out, "images"):        "images",
		filepath.Join(d.out, "packages"):      "packages",
		filepath.Join(d.registry, "payloads"): "registry/payloads",
		filepath.Join(d.registry, "tools"):    "registry/tools",
		filepath.Join(d.out, "guest"):         "bin",
	} {
		if err := os.CopyFS(filepath.Join(st, to), os.DirFS(from)); err != nil {
			return err
		}
	}
	for from, to := range map[string]string{
		filepath.Join(d.keyDir, "cert.der"):          "cert.der",
		filepath.Join(d.build, "shirudo-target"):     "shirudo-target",
		filepath.Join(d.out, "runtime-service.conf"): "runtime-service.conf",
	} {
		if err := copyFile(from, filepath.Join(st, to)); err != nil {
			return err
		}
	}
	if err := d.unit("shirudo.service", filepath.Join(st, "shirudo.service")); err != nil {
		return err
	}
	img := filepath.Join(d.out, "stage.img")
	if err := os.Remove(img); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mkfs := exec.Command(filepath.Join(d.release, "bin/mkfs.ext4"), "-q", "-t", "ext4", "-L", "stage", "-d", st, img, "512M")
	mkfs.Env = append(os.Environ(), "MKE2FS_CONFIG="+filepath.Join(d.release, "e2fsprogs/mke2fs.conf"))
	mkfs.Stdout, mkfs.Stderr = os.Stderr, os.Stderr
	return mkfs.Run()
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
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

// passtSandboxes runs the release's passt as spin-machine boot --net runs
// it, on a socket whose other end is already closed: it makes its user
// namespace and sandbox, reads the end of the socket and exits 0, or dies
// saying what refused it. Where AppArmor refuses an unprivileged process a
// user namespace, passt's own profile lets it make one; installing it takes
// root, so this prints the commands and the reader runs them.
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
