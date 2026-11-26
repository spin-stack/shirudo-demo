// SPDX-License-Identifier: BSD-2-Clause

package console

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ANSI = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Z0-9]|\r`)

// A canonical-mode tty line holds 4095 bytes; leave room for the markers.
const maxLine = 3072

type Console struct {
	conn net.Conn
	buf  []byte
}

func Dial(path string, deadline time.Time) (*Console, error) {
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return &Console{conn: conn}, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Console) Close() error { return c.conn.Close() }

func (c *Console) WaitLogin(deadline time.Time) error {
	for {
		try := time.Now().Add(5 * time.Second)
		if try.After(deadline) {
			try = deadline
		}
		status, _, err := c.Run("stty -echo 2>/dev/null; dmesg -n 1 2>/dev/null; true", try)
		if err == nil && status == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no shell on the console before the deadline: %w", err)
		}
	}
}

func (c *Console) Run(command string, deadline time.Time) (int, string, error) {
	// Interactive bash takes a tab as completion and a newline as Enter.
	if strings.ContainsAny(command, "\n\t") || len(command) > maxLine {
		return 0, "", fmt.Errorf("a console command is one line under %d bytes with no tab: %q", maxLine, command)
	}
	tag := make([]byte, 6)
	if _, err := rand.Read(tag); err != nil {
		return 0, "", err
	}
	t := hex.EncodeToString(tag)
	begin, end := "@@B"+t+"@@", "@@E"+t+"@@"
	// Quote-split markers: the tty echo of the line never matches, only the output does.
	line := fmt.Sprintf("printf '%%s\\n' '@@B''%s@@'; %s; printf '%%s%%s\\n' '@@E''%s@@' \"$?\"\n", t, command, t)
	if err := c.conn.SetDeadline(deadline); err != nil {
		return 0, "", err
	}
	if _, err := c.conn.Write([]byte(line)); err != nil {
		return 0, "", err
	}
	endRe := regexp.MustCompile(regexp.QuoteMeta(end) + `(\d+)\r?\n`)
	chunk := make([]byte, 65536)
	var readErr error
	for {
		if m := endRe.FindSubmatchIndex(c.buf); m != nil {
			status, err := strconv.Atoi(string(c.buf[m[2]:m[3]]))
			if err != nil {
				return 0, "", err
			}
			text := ANSI.ReplaceAll(c.buf[:m[0]], nil)
			c.buf = c.buf[m[1]:]
			if b := bytes.LastIndex(text, []byte(begin)); b >= 0 {
				text = text[b+len(begin):]
			}
			return status, string(bytes.TrimLeft(text, "\n")), nil
		}
		var ne net.Error
		switch {
		case errors.As(readErr, &ne) && ne.Timeout():
			return 0, "", fmt.Errorf("%q: no end marker before the deadline; last output:\n%s", command, tail(c.buf, 2000))
		case readErr != nil:
			return 0, "", fmt.Errorf("console: %w", readErr)
		}
		var n int
		n, readErr = c.conn.Read(chunk)
		c.buf = append(c.buf, chunk[:n]...)
	}
}

func tail(b []byte, n int) string {
	b = ANSI.ReplaceAll(b, nil)
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
