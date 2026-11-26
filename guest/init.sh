#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
# What the machine booted into: the line, lockdown, the TPM, both init runs and
# shirudo's own report. Exits 0 only when status says ok.
S=/run/shirudo/runtime/usr/bin/shirudo
echo "## uname -r"; uname -r
echo "## /proc/cmdline"; cat /proc/cmdline
echo "## lockdown"; cat /sys/kernel/security/lockdown
echo "## lsm"; cat /sys/kernel/security/lsm; echo
echo "## TPM"; ls -l /dev/tpm0 /dev/tpmrm0
echo "## journalctl -b -u shirudo-initrd.service -u shirudo.service"
journalctl -b -u shirudo-initrd.service -u shirudo.service --no-pager -o short-monotonic
echo "## shirudo status"
"$S" status; rc=$?
echo "## shirudo image status"
"$S" image status
echo "## shirudo status --show-full (keyrings, TPM measurement)"
"$S" status --show-full | sed -n '/Keyrings\|keyring\|TPM measurement\|Bound in/,+6p'
echo "## shirudo status exit $rc"
exit $rc
