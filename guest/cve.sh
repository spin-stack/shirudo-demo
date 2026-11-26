#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
# CVE-2025-24813 as upatch.dev publishes it: the registry's setup and payload,
# the upatch built and signed with this run's key. Expects 0, 10, 0.
set -u
S=/run/shirudo/runtime/usr/bin/shirudo
R=/opt/demo/registry
P=$R/payloads/cve-2025-24813
W=/opt/demo/work/cve-2025-24813
rm -rf "$W"; mkdir -p "$W"
tar -xzf /opt/demo/packages/cve-2025-24813-1.0.0.bee -C "$W"
up=$(find "$W" -path '*/upatches/*.upatch' ! -name '*.unload.upatch')
unload=${up%.upatch}.unload.upatch

if ! curl -fsS -o /dev/null http://127.0.0.1:8080/ 2>/dev/null; then
	echo "## setup (the registry's, unchanged; downloads answered from the stage disk)"
	PATH=/opt/demo/offline:$PATH "$P/setup" >"$W/env" || exit 1
else
	echo "## setup: Tomcat already running"
	"$S" upatch unload "$unload" >/dev/null 2>&1
	printf 'TOMCAT_URL=http://127.0.0.1:8080\nTOMCAT_APP_WORK=%s\n' \
		/opt/upatch-payload/cve-2025-24813/apache-tomcat-9.0.98/work/Catalina/localhost/ROOT >"$W/env"
fi
cat "$W/env"
set -a; . "$W/env"; set +a

probe() { "$P/payload"; rc=$?; echo "payload exit $rc"; }
echo "## payload, before the upatch"; probe; before=$rc
echo "## shirudo upatch load $(basename "$up")"; "$S" upatch load "$up" || exit 1
echo "## shirudo upatch list"; "$S" upatch list
echo "## payload, upatch loaded"; probe; during=$rc
echo "## shirudo upatch unload $(basename "$unload")"; "$S" upatch unload "$unload" || exit 1
echo "## payload, after the unload"; probe; after=$rc
echo "## result: before=$before loaded=$during after=$after"
[ "$before" = 0 ] && [ "$during" = 10 ] && [ "$after" = 0 ]
