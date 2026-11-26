#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
# shirudo's kernel-obs: load it, make the kernel do four things, read the
# records back with `shirudo upatch map`.
set -u
S=/run/shirudo/runtime/usr/bin/shirudo
W=/opt/demo/work/kernel-obs
rm -rf "$W"; mkdir -p "$W"
tar -xzf /opt/demo/packages/kernel-obs-1.0.0.bee -C "$W"
up=$(find "$W" -path '*/upatches/kernel-obs.upatch')
"$S" upatch list | grep -q '^ *kernel-obs ' || { echo "## shirudo upatch load kernel-obs.upatch"; "$S" upatch load "$up" || exit 1; }
echo "## shirudo upatch list"; "$S" upatch list

# Each read drains the table's interval, so the four actions run right before
# the reads, and each table holds what happened since it was last read.
for t in exec file dns net; do "$S" upatch map kernel-obs "$t" --json >/dev/null; done

echo "## an exec: /usr/bin/id"; /usr/bin/id
echo "## a read of /etc/shadow"; head -c0 /etc/shadow && echo read
echo "## a DNS lookup: demo.example, answered by a resolver on 127.0.0.1:53"
python3 /opt/demo/dns.py
echo "## a connection: 127.0.0.1:8080"
python3 -c 'import socket; s=socket.create_connection(("127.0.0.1", 8080), 5); s.sendall(b"GET / HTTP/1.0\r\n\r\n"); print(s.recv(12)); s.close()' ||
	echo "nothing listens on 8080"

rc=0
for t in exec file dns net; do
	echo "## shirudo upatch map kernel-obs $t --json"
	"$S" upatch map kernel-obs "$t" --json >"$W/$t.json" || rc=1
	python3 /opt/demo/obs-summary.py <"$W/$t.json" || rc=1
done
echo "## shirudo upatch map kernel-obs"
"$S" upatch map kernel-obs
echo "## checks"
id=$(readlink -f /usr/bin/id)
grep -q "\"path\":\"$id\"" "$W/exec.json" && echo "exec: /usr/bin/id ($id) recorded" || rc=1
grep -q 'shadow' "$W/file.json" && echo "file: /etc/shadow recorded" || rc=1
grep -q '"name":"demo.example"' "$W/dns.json" && echo "dns: demo.example recorded" || rc=1
grep -q '8080' "$W/net.json" && echo "net: port 8080 recorded" || rc=1
exit $rc
