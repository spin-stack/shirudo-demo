// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ext4IocResizeFS is EXT4_IOC_RESIZE_FS, _IOW('f', 16, __u64).
const ext4IocResizeFS = 0x40086610

// provision does what "Place the images on the target" and "Install the unit
// drop-ins" in shirudo's boot-integration guide do, and installs the Python
// the registry's payloads are written in.
func provision() error {
	heading("grow / to the disk")
	if err := growRoot(); err != nil {
		return err
	}

	heading("shirudo's images, certificate, binary and units")
	images := filepath.Join(stage, "images")
	if err := os.MkdirAll("/var/lib/shirudo/images", 0o700); err != nil {
		return err
	}
	if err := os.Chmod("/var/lib/shirudo/images", 0o700); err != nil {
		return err
	}
	for _, f := range []string{
		"runtime.img", "runtime.verity", "runtime.roothash",
		"initial.img", "initial.verity", "initial.roothash", "initial.p7s",
	} {
		if err := install(filepath.Join(images, f), filepath.Join("/var/lib/shirudo/images", f), 0o644); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		from, to string
		mode     os.FileMode
	}{
		{"cert.der", "/var/lib/shirudo/keys/cert.der", 0o644},
		{"shirudo-target", "/usr/bin/shirudo", 0o755},
		{"shirudo.service", "/etc/systemd/system/shirudo.service", 0o644},
		{"runtime-service.conf", "/etc/systemd/system/shirudo.service.d/runtime.conf", 0o644},
	} {
		if err := install(filepath.Join(stage, f.from), f.to, f.mode); err != nil {
			return err
		}
	}
	if err := run(nil, "systemctl", "enable", "shirudo.service"); err != nil {
		return err
	}

	heading("apt-get install python3")
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	if err := run(env, "apt-get", "-q", "update"); err != nil {
		return err
	}
	if err := run(env, "apt-get", "-q", "-o", "Dpkg::Use-Pty=0", "install", "-y", "--no-install-recommends", "python3"); err != nil {
		return err
	}
	unix.Sync()
	fmt.Println("provisioned")
	return nil
}

// growRoot grows / to fill /dev/vda. The base image is a 1 GiB file system
// with ~100 MiB free, too small for Java. resize2fs cannot grow it: it opens
// /dev/vda for writing, which this kernel refuses for a mounted device
// (CONFIG_BLK_DEV_WRITE_MOUNTED is off, as shirudo's hardening asks). ext4's
// own online resize, the ioctl on the mount, can.
func growRoot() error {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		return err
	}
	b, err := os.ReadFile("/sys/class/block/vda/size")
	if err != nil {
		return err
	}
	sectors, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return err
	}
	blocks := sectors * 512 / uint64(st.Bsize)
	root, err := os.Open("/")
	if err != nil {
		return err
	}
	defer root.Close()
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, root.Fd(), ext4IocResizeFS, uintptr(unsafe.Pointer(&blocks))); errno != 0 {
		return fmt.Errorf("EXT4_IOC_RESIZE_FS to %d blocks: %w", blocks, errno)
	}
	if err := unix.Statfs("/", &st); err != nil {
		return err
	}
	fmt.Printf("/: %d MiB, %d MiB free\n", st.Blocks*uint64(st.Bsize)>>20, st.Bavail*uint64(st.Bsize)>>20)
	return nil
}

func install(from, to string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return err
	}
	fmt.Println(to)
	return out.Close()
}
