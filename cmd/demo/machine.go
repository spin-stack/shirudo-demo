// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spin-stack/shirudo-demo/internal/console"
)

type disk struct {
	img, serial string
	readonly    bool
}

func (k disk) dev() string { return "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_" + k.serial }

func (d *demo) stageDisk() disk {
	return disk{img: filepath.Join(d.out, "stage.img"), serial: "stage", readonly: true}
}

// Per-boot socket names: an exiting QEMU unlinks its sockets, even if the next boot reuses the name.
func (d *demo) pidFile() string     { return filepath.Join(d.run, "machine") }
func (d *demo) consoleSock() string { return filepath.Join(d.run, d.name+".console.sock") }
func (d *demo) qmpSock() string     { return filepath.Join(d.run, d.name+".qmp.sock") }

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

func (d *demo) start(name, root string, k disk, flags ...string) (*console.Console, error) {
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
	args := append([]string{"boot", "--release", d.release, "--disk", root,
		"--memory", strconv.Itoa(d.memoryMB), "--cpus", strconv.Itoa(d.cpus), "--net",
		"--hotplug-disks", "1", "--qmp", d.qmpSock(),
		"--console", "unix:" + d.consoleSock() + ",server=on,wait=off,logfile=" + filepath.Join(d.run, name+".console.log")},
		flags...)
	m := exec.Command(d.machineCmd, args...)
	m.Stdout, m.Stderr = log, log
	m.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := m.Start(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(d.pidFile(), []byte(strconv.Itoa(m.Process.Pid)+" "+name), 0o644); err != nil {
		return nil, err
	}
	say("%s: booting (pid %d); console %s", name, m.Process.Pid, d.consoleSock())
	// Reaped here, or a machine that exits stays a zombie while demo runs,
	// and kill(pid, 0) in pid() and shutdown() still finds it.
	exited := make(chan struct{})
	go func() { _ = m.Wait(); close(exited) }()
	deadline := time.Now().Add(5 * time.Minute)
	var con *console.Console
	for {
		if con, err = console.Dial(d.consoleSock(), time.Now().Add(time.Second)); err == nil {
			break
		}
		select {
		case <-exited:
			out, _ := os.ReadFile(log.Name())
			return nil, fmt.Errorf("%s exited before its console opened: %s", name, strings.TrimSpace(string(out)))
		default:
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
	if err := con.WaitLogin(deadline); err != nil {
		con.Close()
		return nil, fmt.Errorf("%w; the machine said: %s", err, filepath.Join(d.run, name+".out"))
	}
	if err := d.attach(con, k); err != nil {
		con.Close()
		return nil, err
	}
	return con, nil
}

func (d *demo) attach(con *console.Console, k disk) error {
	args := []string{"attach", "--qmp", d.qmpSock(), "--release", d.release,
		"--disk", k.img, "--disk-format", "raw", "--disk-serial", k.serial}
	mount := "mount "
	if k.readonly {
		args = append(args, "--disk-readonly")
		mount += "-o ro "
	}
	if err := cmd("", d.machineCmd, args...); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if status, _, err := con.Run("test -e "+k.dev(), deadline); err != nil {
			return err
		} else if status == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the guest has no %s after the attach", k.dev())
		}
		time.Sleep(time.Second)
	}
	if err := guest(con, "mkdir -p /opt/demo && "+mount+k.dev()+" /opt/demo", time.Minute); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.run, k.serial+".attached"), nil, 0o644)
}

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
	return d.attach(con, d.stageDisk())
}

func (d *demo) connect() (*console.Console, error) {
	if _, ok := d.pid(); !ok {
		return nil, errors.New("the machine is not running: demo boot")
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

// shutdown powers the guest off through ACPI, with no console open: a poweroff
// typed at the console sometimes stalled the guest on its serial tty's hangup.
func (d *demo) shutdown() error {
	pid, ok := d.pid()
	if !ok {
		return nil
	}
	if err := d.qmp("system_powerdown"); err != nil {
		return err
	}
	for range 60 {
		if syscall.Kill(pid, 0) != nil {
			return os.Remove(d.pidFile())
		}
		time.Sleep(time.Second)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	// spin-machine stops QEMU before it exits; until then QEMU holds the disk's lock.
	for range 30 {
		if syscall.Kill(pid, 0) != nil {
			return os.Remove(d.pidFile())
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the machine (pid %d) is still running 30 s after SIGTERM", pid)
}

func (d *demo) overlay(path, size string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	args := []string{"create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", filepath.Join(d.release, "image/rootfs.qcow2"), path}
	if size != "" {
		args = append(args, size)
	}
	return cmd("", filepath.Join(d.release, "bin/qemu-img"), args...)
}

const autologin = "systemd.set_credential=agetty.autologin:root"

// oneShot boots a new overlay of the base image without shirudo, runs one
// demo-guest step with k at /opt/demo, and powers off.
func (d *demo) oneShot(name, root, size string, k disk) error {
	if _, ok := d.pid(); ok {
		return errors.New("the machine is running: demo stop")
	}
	if err := d.overlay(root, size); err != nil {
		return err
	}
	con, err := d.start(name, root, k, "--append", autologin)
	if err != nil {
		return err
	}
	defer con.Close()
	if err := d.record(con, name, name); err != nil {
		return err
	}
	if err := guest(con, "umount /opt/demo", time.Minute); err != nil {
		return err
	}
	con.Close()
	return d.shutdown()
}

func (d *demo) operator() error {
	say("the operator's disk: the release's shirudo and operator half, the registry, demo-guest")
	if err := d.opDisk(); err != nil {
		return err
	}
	root := filepath.Join(d.work, "operator.qcow2")
	if err := d.oneShot("operator", root, "", disk{img: d.opImg(), serial: "op"}); err != nil {
		return err
	}
	if err := os.Remove(root); err != nil {
		return err
	}
	return d.opResults()
}

func (d *demo) provision() error {
	say("provisioning a fresh disk over the release's base image")
	return d.oneShot("provision", filepath.Join(d.work, "disk.qcow2"), "8G", d.stageDisk())
}

func (d *demo) boot() error {
	if _, ok := d.pid(); ok {
		say("the shirudo machine already runs")
		return nil
	}
	tpm := filepath.Join(d.work, "tpm")
	if err := os.MkdirAll(tpm, 0o700); err != nil {
		return err
	}
	con, err := d.start("shirudo", filepath.Join(d.work, "disk.qcow2"), d.stageDisk(), "--lockdown", "shirudo", "--tpm", tpm, "--initrd", filepath.Join(d.out, "initrd.img"),
		"--append", autologin+" systemd.show_status=true")
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
	return d.shutdown()
}

// qmp runs one QMP command on the running machine.
func (d *demo) qmp(command string) error {
	c, err := net.DialTimeout("unix", d.qmpSock(), 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	r := bufio.NewReader(c)
	if _, err := r.ReadString('\n'); err != nil {
		return err
	}
	for _, cmd := range []string{"qmp_capabilities", command} {
		if _, err := fmt.Fprintf(c, `{"execute":%q}`+"\n", cmd); err != nil {
			return err
		}
		var reply string
		for reply == "" || strings.Contains(reply, `"event"`) {
			if reply, err = r.ReadString('\n'); err != nil {
				return err
			}
		}
		if !strings.Contains(reply, `"return"`) {
			return fmt.Errorf("qmp %s: %s", cmd, strings.TrimSpace(reply))
		}
	}
	return nil
}
