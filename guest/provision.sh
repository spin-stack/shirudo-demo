#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
#
# Runs as root in the guest, from the stage disk, before the first boot with
# enforcement: what "Place the images on the target" and "Install the unit
# drop-ins" in shirudo's boot-integration guide do, plus the demo's files.
set -eu
s=$(dirname "$0")

# The base image is a 1 GiB file system with ~100 MiB free, on an 8 GiB disk.
# resize2fs cannot grow it: it opens /dev/vda for writing, which this kernel
# refuses for a mounted device (CONFIG_BLK_DEV_WRITE_MOUNTED is off, as shirudo's
# hardening asks). ext4's own online resize, the ioctl on the mount, can.
bs=$(stat -f -c %S /)
perl -e 'use Fcntl; sysopen(my $f, "/", O_RDONLY) or die "open /: $!\n";
	ioctl($f, 0x40086610, pack("Q", $ARGV[0])) or die "EXT4_IOC_RESIZE_FS: $!\n"' \
	$(( $(blockdev --getsize64 /dev/vda) / bs ))

install -d -m 0700 -o root -g root /var/lib/shirudo/images
install -m 0644 "$s"/images/runtime.img "$s"/images/runtime.verity "$s"/images/runtime.roothash /var/lib/shirudo/images/
install -m 0644 "$s"/images/initial.img "$s"/images/initial.verity "$s"/images/initial.roothash "$s"/images/initial.p7s \
	/var/lib/shirudo/images/
install -D -m 0644 "$s"/cert.der /var/lib/shirudo/keys/cert.der

install -m 0755 "$s"/shirudo-target /usr/bin/shirudo
install -m 0644 "$s"/shirudo.service /etc/systemd/system/shirudo.service
install -D -m 0644 "$s"/runtime-service.conf /etc/systemd/system/shirudo.service.d/runtime.conf
systemctl enable shirudo.service

# The payload's setup installs Java with apt when it is missing, and the payload
# is Python; the guest has neither and no network, so Ubuntu's packages and what
# they need come from the stage disk.
missing=""
for deb in "$s"/debs/*.deb; do
	pkg=$(dpkg-deb -f "$deb" Package)
	dpkg-query -W -f '${Status}' "$pkg" 2>/dev/null | grep -q 'install ok installed' || missing="$missing $deb"
done
# dpkg -i does not order pre-dependencies, so a pass installs what it can and
# the next retries the rest.
for _ in 1 2 3 4; do
	[ -n "$missing" ] || break
	dpkg -i $missing >/dev/null 2>&1 || true
	left=""
	for deb in $missing; do
		dpkg-query -W -f '${Status}' "$(dpkg-deb -f "$deb" Package)" 2>/dev/null | grep -q 'install ok installed' || left="$left $deb"
	done
	missing=$left
done
[ -z "$missing" ] || { dpkg -i $missing; exit 1; }
command -v java >/dev/null && command -v python3 >/dev/null

rm -rf /opt/demo
mkdir -p /opt/demo
cp -a "$s"/demo/. /opt/demo/
sync
echo provisioned
