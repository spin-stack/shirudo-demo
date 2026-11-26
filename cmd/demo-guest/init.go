// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"fmt"
	"os"
	"strings"
)

func initStatus() error {
	for _, f := range []struct{ title, path string }{
		{"kernel", "/proc/sys/kernel/osrelease"},
		{"/proc/cmdline", "/proc/cmdline"},
		{"lockdown", "/sys/kernel/security/lockdown"},
		{"lsm", "/sys/kernel/security/lsm"},
	} {
		heading("%s", f.title)
		b, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		fmt.Println(strings.TrimSpace(string(b)))
	}
	heading("TPM")
	for _, dev := range []string{"/dev/tpm0", "/dev/tpmrm0"} {
		fi, err := os.Stat(dev)
		if err != nil {
			return err
		}
		fmt.Println(dev, fi.Mode())
	}
	for _, c := range [][]string{
		{"ip", "addr"},
		{"ip", "route"},
		{"cat", "/etc/resolv.conf"},
		{"getent", "hosts", lookup},
	} {
		heading("%s", strings.Join(c, " "))
		if err := run(nil, c[0], c[1:]...); err != nil {
			return err
		}
	}
	heading("journalctl -b -u shirudo-initrd.service -u shirudo.service")
	if err := run(nil, "journalctl", "-b", "-u", "shirudo-initrd.service", "-u", "shirudo.service",
		"--no-pager", "-o", "short-monotonic"); err != nil {
		return err
	}
	heading("shirudo status --show-full")
	statusErr := run(nil, shirudo, "status", "--show-full")
	heading("shirudo image status")
	if err := run(nil, shirudo, "image", "status"); err != nil {
		return err
	}
	if statusErr != nil {
		return fmt.Errorf("shirudo status: %w", statusErr)
	}
	return nil
}
