#!/usr/bin/env python3
# SPDX-License-Identifier: BSD-2-Clause
# One line per record of a `shirudo upatch map kernel-obs <table> --json`
# document on stdin, without the identity and timestamp fields.
import json
import sys

DROP = {"entry_index", "cgroup_id", "executable", "caller", "owner", "actor",
        "first_seen_unix_nano", "last_seen_unix_nano", "cap_effective", "path_truncated"}
doc = json.load(sys.stdin)
records = doc.get("records", [])
print(f"{len(records)} records")
for r in records:
    print("  " + json.dumps({k: v for k, v in r.items() if k not in DROP}, separators=(",", ":")))
