// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spin-stack/shirudo-demo/internal/console"
)

// The guest finds the stage disk by its serial.
const stageDev = "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_stage"

// Each boot has its own sockets, named for it: a QEMU that is still exiting
// unlinks its own, which were the next boot's when the two shared a name.
// run/machine holds the running boot's pid and name.
func (d *demo) pidFile() string     { return filepath.Join(d.run, "machine") }
func (d *demo) consoleSock() string { return filepath.Join(d.run, d.name+".console.sock") }
func (d *demo) qmpSock() string     { return filepath.Join(d.run, d.name+".qmp.sock") }

// running exits 0 when the machine runs: Taskfile.yml's boot asks it.
func (d *demo) running() error {
	if _, ok := d.pid(); !ok {
		return errors.New("the machine is not running")
	}
	return nil
}

func (d *demo) pid() (int, bool) {
	b, err := os.ReadFile(d.pidFile())
	if err != nil {
		return 0, false
	}
	p, name, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	pid, err := strconv.Atoi(p)
	if err != nil || syscall.Kill(pid, 0) != nil {
		return 0, false
	}
	d.name = name
	return pid, true
}

// start boots the machine in the background, its console on a socket, waits
// for root's shell, and mounts the stage disk at /opt/demo.
func (d *demo) start(name string, flags ...string) (*console.Console, error) {
	d.name = name
	for _, f := range []string{d.consoleSock(), d.qmpSock()} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	log, err := os.Create(filepath.Join(d.run, name+".out"))
	if err != nil {
		return nil, err
	}
	defer log.Close()
	args := append([]string{"boot", "--release", d.release, "--disk", filepath.Join(d.work, "disk.qcow2"),
		"--memory", strconv.Itoa(d.memoryMB), "--cpus", strconv.Itoa(d.cpus), "--net",
		"--hotplug-disks", "1", "--qmp", d.qmpSock(),
		"--console", "unix:" + d.consoleSock() + ",server=on,wait=off,logfile=" + filepath.Join(d.run, name+".console.log")},
		flags...)
	m := exec.Command(d.machineCmd, args...)
	m.Stdout, m.Stderr = log, log
	// Its own session, so the machine outlives this program.
	m.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := m.Start(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(d.pidFile(), []byte(strconv.Itoa(m.Process.Pid)+" "+name), 0o644); err != nil {
		return nil, err
	}
	say("%s: booting (pid %d); console %s", name, m.Process.Pid, d.consoleSock())
	_ = m.Process.Release()
	deadline := time.Now().Add(5 * time.Minute)
	con, err := console.Dial(d.consoleSock(), deadline)
	if err != nil {
		return nil, err
	}
	if err := con.WaitLogin(deadline); err != nil {
		con.Close()
		return nil, fmt.Errorf("%w; the machine said: %s", err, filepath.Join(d.run, name+".out"))
	}
	if err := d.attachStage(con); err != nil {
		con.Close()
		return nil, err
	}
	return con, nil
}

// attachStage hot-plugs stage.img and mounts it read-only at /opt/demo.
func (d *demo) attachStage(con *console.Console) error {
	if err := cmd("", d.machineCmd, "attach", "--qmp", d.qmpSock(), "--release", d.release,
		"--disk", filepath.Join(d.out, "stage.img"), "--disk-format", "raw", "--disk-serial", "stage", "--disk-readonly"); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if status, _, err := con.Run("test -e "+stageDev, deadline); err != nil {
			return err
		} else if status == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the guest has no %s after the attach", stageDev)
		}
		time.Sleep(time.Second)
	}
	if err := guest(con, "mkdir -p /opt/demo && mount -o ro "+stageDev+" /opt/demo", time.Minute); err != nil {
		return err
	}
	return touch(filepath.Join(d.run, "stage.attached"))
}

// refreshStage puts a stage.img built since the machine's attach in place of
// the old one, so a change reaches the running machine without a reboot.
func (d *demo) refreshStage(con *console.Console) error {
	img, err := os.Stat(filepath.Join(d.out, "stage.img"))
	if err != nil {
		return err
	}
	attached, err := os.Stat(filepath.Join(d.run, "stage.attached"))
	if err == nil && !img.ModTime().After(attached.ModTime()) {
		return nil
	}
	say("a new stage disk: attaching it in place of the old one")
	if err := guest(con, "umount /opt/demo", time.Minute); err != nil {
		return err
	}
	if err := cmd("", d.machineCmd, "detach", "--qmp", d.qmpSock(), "--disk-serial", "stage"); err != nil {
		return err
	}
	return d.attachStage(con)
}

// connect opens the running machine's console.
func (d *demo) connect() (*console.Console, error) {
	if _, ok := d.pid(); !ok {
		return nil, errors.New("the machine is not running: task boot")
	}
	deadline := time.Now().Add(time.Minute)
	con, err := console.Dial(d.consoleSock(), deadline)
	if err != nil {
		return nil, err
	}
	if err := con.WaitLogin(deadline); err != nil {
		con.Close()
		return nil, err
	}
	if err := d.refreshStage(con); err != nil {
		con.Close()
		return nil, err
	}
	return con, nil
}

// guest runs a command in the guest with its output on ours.
func guest(con *console.Console, command string, timeout time.Duration) error {
	status, out, err := con.Run(command, time.Now().Add(timeout))
	fmt.Fprint(os.Stderr, out)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("%s: exit %d", command, status)
	}
	return nil
}

// record runs one of demo-guest's steps and keeps what it printed in
// evidence/NAME.txt.
func (d *demo) record(con *console.Console, name, step string) error {
	status, out, err := con.Run("/opt/demo/bin/demo-guest "+step, time.Now().Add(30*time.Minute))
	fmt.Print(out)
	if werr := os.WriteFile(filepath.Join(d.evidence, name+".txt"), []byte(out), 0o644); werr != nil {
		return werr
	}
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("demo-guest %s: exit %d; evidence/%s.txt", step, status, name)
	}
	return nil
}

func (d *demo) shutdown(con *console.Console) error {
	pid, ok := d.pid()
	if !ok {
		return nil
	}
	// The console closes as the guest powers off, so the command never ends.
	_, _, _ = con.Run("systemctl poweroff", time.Now().Add(20*time.Second))
	for range 60 {
		if syscall.Kill(pid, 0) != nil {
			return os.Remove(d.pidFile())
		}
		time.Sleep(time.Second)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return os.Remove(d.pidFile())
}

// provision makes a new 8 GiB disk over the release's base image, boots it
// without shirudo and has demo-guest place shirudo's images and units on it.
func (d *demo) provision() error {
	if _, ok := d.pid(); ok {
		return errors.New("the machine is running: task stop")
	}
	say("provisioning a fresh disk over the release's base image")
	disk := filepath.Join(d.work, "disk.qcow2")
	if err := os.Remove(disk); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := cmd("", filepath.Join(d.release, "bin/qemu-img"), "create", "-q", "-f", "qcow2", "-F", "qcow2",
		"-b", filepath.Join(d.release, "image/rootfs.qcow2"), disk, "8G"); err != nil {
		return err
	}
	con, err := d.start("provision", "--append", "systemd.set_credential=agetty.autologin:root")
	if err != nil {
		return err
	}
	defer con.Close()
	if err := d.record(con, "provision", "provision"); err != nil {
		return err
	}
	return d.shutdown(con)
}

// boot boots the provisioned disk as a shirudo machine: lockdown and keyrings
// as shirudo asks, a TPM, and shirudo's initrd.
func (d *demo) boot() error {
	if _, ok := d.pid(); ok {
		say("the shirudo machine already runs")
		return nil
	}
	tpm := filepath.Join(d.work, "tpm")
	if err := os.MkdirAll(tpm, 0o700); err != nil {
		return err
	}
	con, err := d.start("shirudo", "--lockdown", "shirudo", "--tpm", tpm, "--initrd", filepath.Join(d.out, "initrd.img"),
		"--append", "systemd.set_credential=agetty.autologin:root systemd.show_status=true")
	if err != nil {
		return err
	}
	defer con.Close()
	raw, err := os.ReadFile(filepath.Join(d.run, "shirudo.console.log"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.evidence, "boot-console.log"), console.ANSI.ReplaceAll(raw, nil), 0o644)
}

func (d *demo) init() error {
	con, err := d.connect()
	if err != nil {
		return err
	}
	defer con.Close()
	say("shirudo init, in the initrd and again in the root file system")
	return d.record(con, "init", "init")
}

func (d *demo) cve() error {
	con, err := d.connect()
	if err != nil {
		return err
	}
	defer con.Close()
	say("CVE-2025-24813: payload, load, payload, unload, payload")
	if err := d.record(con, "cve", "cve"); err != nil {
		return err
	}
	say("the registry's own tools/test, as its README runs it")
	return d.record(con, "cve-tools-test", "cve-test")
}

func (d *demo) obs() error {
	con, err := d.connect()
	if err != nil {
		return err
	}
	defer con.Close()
	say("kernel-obs: what the kernel saw")
	return d.record(con, "obs", "obs")
}

func (d *demo) stop() error {
	if _, ok := d.pid(); !ok {
		return nil
	}
	say("stopping the machine")
	con, err := console.Dial(d.consoleSock(), time.Now().Add(10*time.Second))
	if err != nil {
		return err
	}
	defer con.Close()
	return d.shutdown(con)
}

func touch(path string) error {
	now := time.Now()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return err
	}
	return os.Chtimes(path, now, now)
}
