// SPDX-License-Identifier: BSD-2-Clause

package console

import (
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A bash on a terminal that echoes what is typed, as agetty's login shell
// does, behind a Unix socket, as QEMU's chardev serves the console.
func shell(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("needs util-linux's script for a terminal")
	}
	sock := filepath.Join(t.TempDir(), "console.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		f, _ := conn.(*net.UnixConn).File()
		conn.Close()
		cmd := exec.Command("script", "-qfec", "bash --norc --noprofile -i", "/dev/null")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f
		_ = cmd.Start()
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); f.Close() })
	}()
	return sock
}

func TestRunReturnsEachCommandsOutputAndStatus(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second)
	c, err := Dial(shell(t), deadline)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WaitLogin(deadline); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command, out string
		status       int
	}{
		{"echo one; echo two", "one\ntwo\n", 0},
		{"printf '\\033[1mbold\\033[0m\\r\\n'; exit_with() { return $1; }; exit_with 10", "bold\n", 10},
		{"seq 20000 | tail -1", "20000\n", 0},
	} {
		status, out, err := c.Run(tc.command, deadline)
		if err != nil {
			t.Fatalf("%s: %v", tc.command, err)
		}
		if status != tc.status || out != tc.out {
			t.Errorf("%s: status %d, output %q; want %d, %q", tc.command, status, out, tc.status, tc.out)
		}
	}
	if _, _, err := c.Run("echo a\necho b", deadline); err == nil || !strings.Contains(err.Error(), "one line") {
		t.Errorf("a two-line command: %v, want a refusal", err)
	}
}
