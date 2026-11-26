#!/usr/bin/env python3
# SPDX-License-Identifier: BSD-2-Clause
"""Run one shell command in the guest over its serial console.

usage: guest.py SOCKET [--timeout S] [--wait-login] -- COMMAND

The console is a root shell (agetty autologin). The command's output is
printed, and guest.py exits with the command's exit status. --wait-login
waits for the shell to answer, for use right after boot.
"""

import base64
import re
import secrets
import socket
import sys
import time

ANSI = re.compile(rb"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Z0-9]|\r")


def connect(path, deadline):
    while True:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            s.connect(path)
            return s
        except OSError:
            s.close()
            if time.time() > deadline:
                raise
            time.sleep(0.5)


CHUNK = 1024  # a tty in canonical mode holds 4095 bytes of a line


def send(sock, shell, deadline):
    """Type one short line at the guest's shell; return its status and output."""
    tag = secrets.token_hex(6)
    begin, end = f"@@B{tag}@@", f"@@E{tag}@@"
    # The markers are printed split by quotes, so the echoed command line
    # never matches them; only the output does.
    sock.sendall((f"printf '%s\\n' '@@B''{tag}@@'; {shell}; "
                  f"printf '%s%s\\n' '@@E''{tag}@@' \"$?\"\n").encode())
    buf = b""
    while True:
        sock.settimeout(max(1, deadline - time.time()))
        try:
            chunk = sock.recv(65536)
        except socket.timeout:
            raise TimeoutError(f"no end marker before the timeout; last output:\n{ANSI.sub(b'', buf[-2000:]).decode(errors='replace')}")
        if not chunk:
            raise ConnectionError("console closed")
        buf += chunk
        m = re.search(re.escape(end.encode()) + rb"(\d+)", buf)
        if m:
            text = ANSI.sub(b"", buf[:m.start()])
            b = text.rfind(begin.encode())
            out = text[b + len(begin):] if b >= 0 else text
            return int(m.group(1)), out.lstrip(b"\n").decode(errors="replace")


def run(sock, command, deadline):
    """Run a script of any length: it travels base64-encoded, in short lines,
    because a tab or newline typed at an interactive bash would be completion
    or an early Enter."""
    b64 = base64.b64encode(command.encode()).decode()
    if len(b64) <= CHUNK:
        return send(sock, f"bash -c \"$(echo {b64} | base64 -d)\"", deadline)
    tmp = f"/run/guest-{secrets.token_hex(6)}"
    for i in range(0, len(b64), CHUNK):
        status, out = send(sock, f"printf %s {b64[i:i + CHUNK]} >>{tmp}.b64", deadline)
        if status:
            raise RuntimeError(f"could not write {tmp}.b64: {out}")
    return send(sock, f"base64 -d {tmp}.b64 >{tmp}.sh; rm -f {tmp}.b64; bash {tmp}.sh; set -- $?; rm -f {tmp}.sh; (exit $1)",
                deadline)


def main():
    args = sys.argv[1:]
    if not args or "--" not in args:
        sys.exit(__doc__)
    path, opts, command = args[0], args[1:args.index("--")], " ".join(args[args.index("--") + 1:])
    timeout = 600
    wait_login = False
    i = 0
    while i < len(opts):
        if opts[i] == "--timeout":
            timeout = int(opts[i + 1])
            i += 2
        elif opts[i] == "--wait-login":
            wait_login = True
            i += 1
        else:
            sys.exit(f"unknown option {opts[i]}")
    deadline = time.time() + timeout
    sock = connect(path, deadline)
    if wait_login:
        while True:
            try:
                status, _ = run(sock, "stty -echo 2>/dev/null; dmesg -n 1 2>/dev/null; true",
                                min(deadline, time.time() + 5))
                if status == 0:
                    break
            except TimeoutError:
                if time.time() > deadline:
                    raise
    status, out = run(sock, command, deadline)
    sys.stdout.write(out)
    sys.stdout.flush()
    sys.exit(status)


if __name__ == "__main__":
    main()
