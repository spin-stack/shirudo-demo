#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
#
# ./demo.sh [step...]   every step, in order, when none is named.
# MACHINE_RELEASE names an unpacked spin-machine release tree (README).

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/versions.env"

WORK=${WORK:-$here/work}
EVIDENCE=${EVIDENCE:-$here/evidence}
MACHINE_RELEASE=${MACHINE_RELEASE:-}
MEMORY_MB=${MEMORY_MB:-4096}
CPUS=${CPUS:-4}
export TMPDIR=${TMPDIR:-$WORK/tmp}

SRC=$WORK/src
BUILD=$SRC/shirudo
REGISTRY=$SRC/upatch-registry
KEYS=$WORK/keys
OUT=$WORK/out
RUN=$WORK/run
STEPS=(check shirudo keys images initrd packages stage provision boot init cve obs stop)

say() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }
die() { printf 'demo: %s\n' "$*" >&2; exit 1; }

machine() { "${SPIN_MACHINE:-$MACHINE_RELEASE/bin/spin-machine}" "$@"; }
mkfs() { MKE2FS_CONFIG=$MACHINE_RELEASE/e2fsprogs/mke2fs.conf "$MACHINE_RELEASE/bin/mkfs.ext4" "$@"; }
guest() { python3 "$here/lib/guest.py" "$RUN/console.sock" "$@"; }

# The operator side runs in shirudo's own build container (testing/container),
# which this checkout's run.sh builds once. Every path is mounted at itself.
operator()
{
	local image
	image=$("$BUILD/testing/container/run.sh" --image)
	docker image inspect "$image" >/dev/null 2>&1 ||
		BUILDX_BUILDER=${BUILDX_BUILDER:-default} "$BUILD/testing/container/run.sh" -- true
	docker run --rm --init --user "$(id -u):$(id -g)" --ulimit memlock=-1:-1 \
		-e HOME=/tmp -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e 'GIT_CONFIG_VALUE_0=*' \
		-e "PATH=$BUILD:$BUILD/tools/shirudo-build:$BUILD/tools/gen-image:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
		-v "$WORK:$WORK" -v "$MACHINE_RELEASE:$MACHINE_RELEASE:ro" -w "$WORK" \
		"$image" "$@"
}

clone()
{
	local repo=$1 ref=$2 dir=$3
	if [ ! -d "$dir/.git" ]; then
		git init -q "$dir"
		git -C "$dir" remote add origin "$repo"
	fi
	if [ "$(git -C "$dir" rev-parse -q --verify HEAD 2>/dev/null)" != "$ref" ]; then
		git -C "$dir" fetch -q --depth 1 origin "$ref"
		git -C "$dir" checkout -q --detach FETCH_HEAD
	fi
}

kernel_release() { strings "$MACHINE_RELEASE/kernel/vmlinux" | sed -n 's/^Linux version \([^ ]*\) .*/\1/p' | head -1; }

# --- steps -----------------------------------------------------------------

step_check()
{
	[ -n "$MACHINE_RELEASE" ] || die "set MACHINE_RELEASE to an unpacked spin-machine release tree (README)"
	MACHINE_RELEASE=$(cd "$MACHINE_RELEASE" && pwd)
	local f
	for f in bin/swtpm bin/qemu-img bin/mkfs.ext4 e2fsprogs/mke2fs.conf kernel/vmlinux image/rootfs.qcow2; do
		[ -e "$MACHINE_RELEASE/$f" ] || die "$MACHINE_RELEASE has no $f: it predates the TPM (README, 'What you need')"
	done
	[ -x "${SPIN_MACHINE:-$MACHINE_RELEASE/bin/spin-machine}" ] ||
		die "no spin-machine: set SPIN_MACHINE to the one built from the release's tag (README)"
	for f in docker git python3 curl strings perl; do
		command -v "$f" >/dev/null || die "$f is not installed"
	done
	[ -r /dev/kvm ] && [ -w /dev/kvm ] || die "/dev/kvm is not usable by $(id -un)"
	mkdir -p "$WORK" "$TMPDIR" "$EVIDENCE" "$OUT" "$RUN"
	say "release $MACHINE_RELEASE, kernel $(kernel_release)"
}

step_shirudo()
{
	clone "$SHIRUDO_REPO" "$SHIRUDO_REF" "$BUILD"
	clone "$REGISTRY_REPO" "$REGISTRY_REF" "$REGISTRY"
	if [ -x "$BUILD/shirudo" ] && [ -x "$BUILD/shirudo-target" ] && [ -x "$BUILD/tools/shirudo-build/shirudo-build" ]; then
		say "shirudo already built at $SHIRUDO_REF"
		return
	fi
	say "building shirudo $SHIRUDO_REF in its container, against the release kernel's BTF"
	operator sh -c "cd '$BUILD' && make -j\$(nproc) BPF_VMLINUX_BTF='$MACHINE_RELEASE/kernel/vmlinux' \
		&& make -j\$(nproc) BPF_VMLINUX_BTF='$MACHINE_RELEASE/kernel/vmlinux' target shirudo-build"
	operator "$BUILD/shirudo-target" version
}

step_keys()
{
	if [ -s "$KEYS/cert.der" ]; then
		say "operator key already made: $KEYS"
		return
	fi
	say "making this run's operator key (never committed)"
	mkdir -p "$KEYS"
	chmod 0700 "$KEYS"
	operator shirudo genkey --keys-dir="$KEYS"
	operator shirudo bpm key fingerprint "$KEYS/cert.der"
}

step_images()
{
	local img=$OUT/images
	if [ -s "$img/runtime.roothash" ] && [ -s "$img/initial.p7s" ]; then
		say "config and runtime images already built"
		return
	fi
	say "building the signed config image and the dm-verity runtime image"
	rm -rf "${img:?}" "${OUT:?}/config-root"
	mkdir -p "$img" "$OUT/config-root/etc/shirudo"
	printf 'eventlog.max_bytes = 0\n' >"$OUT/config-root/etc/shirudo/shirudo.conf"
	cat >"$OUT/runtime.conf" <<-'EOF'
	config.image = /var/lib/shirudo/images/initial
	config.verification_key = /run/shirudo/runtime/etc/shirudo/config-cert.der
	config.pcr = 14
	tpm.tcti = device:/dev/tpmrm0
	EOF
	operator sh -c "cd '$img' && shirudo-gen-image --source '$OUT/config-root' --output initial --keys-dir '$KEYS' --kind config \
		&& shirudo image verify initial --cert '$KEYS/cert.der' \
		&& shirudo-gen-runtime --binary '$BUILD/shirudo-target' --bootstrap-conf '$OUT/runtime.conf' --cert '$KEYS/cert.der' \
			--config-cert '$KEYS/cert.der' --output runtime --keys-dir '$KEYS' --unit-output '$OUT/runtime-service.conf'"
	printf 'runtime.image = /var/lib/shirudo/images/runtime\nruntime.root_hash = %s\n' \
		"$(cat "$img/runtime.roothash")" >"$OUT/uki-bootstrap.conf"
	chmod 0600 "$OUT/uki-bootstrap.conf"
}

step_initrd()
{
	local kver unit
	kver=$(kernel_release)
	if [ -s "$OUT/initrd.img" ] && [ "$OUT/initrd.img" -nt "$OUT/uki-bootstrap.conf" ]; then
		say "initrd already built for $kver"
		return
	fi
	say "building the shirudo initrd for $kver with dracut, in Ubuntu 26.04's systemd"
	docker build -q --build-arg "UBUNTU_IMAGE=$UBUNTU_IMAGE" -t shirudo-demo-initrd "$here/initrd" >/dev/null
	unit=$OUT/shirudo-initrd.service
	sed 's|@bindir@|/usr/bin|g' "$BUILD/systemd/shirudo-initrd.service.in" >"$unit"
	docker run --rm --user "$(id -u):$(id -g)" -e TMPDIR=/tmp -v "$WORK:$WORK" shirudo-demo-initrd \
		"$BUILD/tools/gen-initrd/shirudo-gen-initrd" --kver "$kver" --no-kernel \
		--binary "$BUILD/shirudo-target" --binary-path /usr/bin/shirudo \
		--cert "$KEYS/cert.der" --bootstrap-conf "$OUT/uki-bootstrap.conf" \
		--unit "$unit" --module-dir "$BUILD/tools/gen-initrd" --output "$OUT/initrd.img" -- \
		--no-hostonly --no-hostonly-cmdline --zstd
	docker run --rm --user "$(id -u):$(id -g)" -v "$WORK:$WORK" shirudo-demo-initrd \
		"$BUILD/tools/gen-initrd/shirudo-gen-initrd" --check "$OUT/initrd.img"
	ls -l "$OUT/initrd.img" >&2
}

step_packages()
{
	local pk=$OUT/packages
	mkdir -p "$pk"
	if [ ! -s "$REGISTRY/build/packages/cve-2025-24813-1.0.0.bee" ]; then
		say "building and signing CVE-2025-24813's upatch with the registry's tools/build"
		operator env SHIRUDO="$BUILD/shirudo" "$REGISTRY/tools/build" "$BUILD" "$KEYS" 1.0.0 \
			"$REGISTRY/upatches/cve-2025-24813"
	fi
	cp "$REGISTRY/build/packages/cve-2025-24813-1.0.0.bee" "$pk/"
	if [ ! -s "$pk/kernel-obs-1.0.0.bee" ]; then
		say "building and signing shirudo's kernel-obs package"
		# Staged the way the registry's tools/build stages a package: a copy,
		# with shirudo's vendor/ and bpf/ beside its sources.
		rm -rf "${OUT:?}/kernel-obs"
		cp -R "$BUILD/upatches/kernel-obs" "$OUT/kernel-obs"
		ln -s "$BUILD/vendor" "$OUT/kernel-obs/vendor"
		ln -s "$BUILD/bpf" "$OUT/kernel-obs/bpf"
		operator sh -c "cd '$pk' && shirudo bpm build '$OUT/kernel-obs' --version 1.0.0 --keys-dir '$KEYS'"
	fi
	ls -l "$pk" >&2
}

step_stage()
{
	local st=$OUT/stage v=9.0.98 base=https://archive.apache.org/dist/tomcat/tomcat-9/v9.0.98/bin
	say "staging what the guest gets: images, units, the packages, the payload and its downloads"
	mkdir -p "$WORK/cache/debs"
	if [ ! -s "$WORK/cache/apache-tomcat-$v.tar.gz" ]; then
		curl -fsSL -o "$WORK/cache/apache-tomcat-$v.tar.gz" "$base/apache-tomcat-$v.tar.gz"
		curl -fsSL -o "$WORK/cache/apache-tomcat-$v.tar.gz.sha512" "$base/apache-tomcat-$v.tar.gz.sha512"
	fi
	(cd "$WORK/cache" && printf '%s  %s\n' "$(cut -d' ' -f1 "apache-tomcat-$v.tar.gz.sha512")" "apache-tomcat-$v.tar.gz" | sha512sum -c --quiet -)
	if ! ls "$WORK/cache/debs/"openjdk-21-jre-headless_*.deb >/dev/null 2>&1; then
		docker run --rm -v "$WORK/cache/debs:/debs" "$UBUNTU_IMAGE" sh -c \
			"apt-get -qq update && apt-get -qq install -y --download-only --no-install-recommends openjdk-21-jre-headless python3 >/dev/null \
			 && cp /var/cache/apt/archives/*.deb /debs/ && chown -R $(id -u):$(id -g) /debs"
	fi
	rm -rf "${st:?}"
	mkdir -p "$st/images" "$st/demo/cache" "$st/demo/packages" "$st/demo/registry" "$st/debs"
	cp "$OUT"/images/runtime.{img,verity,roothash} "$OUT"/images/initial.{img,verity,roothash,p7s} "$st/images/"
	cp "$KEYS/cert.der" "$BUILD/shirudo-target" "$OUT/runtime-service.conf" "$st/"
	sed 's|@bindir@|/usr/bin|g' "$BUILD/systemd/shirudo.service.in" >"$st/shirudo.service"
	cp "$WORK"/cache/debs/*.deb "$st/debs/"
	cp "$WORK"/cache/apache-tomcat-$v.tar.gz* "$st/demo/cache/"
	cp "$OUT"/packages/*.bee "$st/demo/packages/"
	cp -a "$REGISTRY/payloads" "$REGISTRY/tools" "$st/demo/registry/"
	cp -a "$here/guest/." "$st/"
	cp -a "$here/guest/." "$st/demo/"
	rm -f "$OUT/stage.img"
	mkfs -q -t ext4 -L stage -d "$st" "$OUT/stage.img" 1G
}

boot_machine()
{
	# boot_machine NAME [spin-machine boot flags...]: start the machine in the
	# background, its console on a socket, and wait for root's shell.
	local name=$1
	shift
	mkdir -p "$RUN"
	rm -f "$RUN/console.sock" "$RUN/qmp.sock"
	machine boot --release "$MACHINE_RELEASE" --disk "$WORK/disk.qcow2" --memory "$MEMORY_MB" --cpus "$CPUS" \
		--hotplug-disks 1 --qmp "$RUN/qmp.sock" --console "unix:$RUN/console.sock,server=on,wait=off,logfile=$RUN/$name.console.log" \
		--append "systemd.set_credential=agetty.autologin:root ${APPEND:-}" "$@" \
		>"$RUN/$name.out" 2>&1 &
	echo $! >"$RUN/machine.pid"
	say "$name: booting (pid $(cat "$RUN/machine.pid")); console $RUN/console.sock"
	guest --wait-login --timeout 300 -- true
}

stop_machine()
{
	[ -s "$RUN/machine.pid" ] || return 0
	local pid
	pid=$(cat "$RUN/machine.pid")
	guest --timeout 20 -- 'systemctl poweroff' >/dev/null 2>&1 || true
	for _ in $(seq 60); do
		kill -0 "$pid" 2>/dev/null || break
		sleep 1
	done
	kill "$pid" 2>/dev/null || true
	rm -f "$RUN/machine.pid"
}

step_provision()
{
	if [ -s "$WORK/provisioned" ] && [ "$WORK/provisioned" -nt "$OUT/stage.img" ]; then
		say "the guest disk is already provisioned"
		return
	fi
	say "provisioning a fresh disk over the release's base image"
	rm -f "$WORK/disk.qcow2"
	"$MACHINE_RELEASE/bin/qemu-img" create -q -f qcow2 -F qcow2 -b "$MACHINE_RELEASE/image/rootfs.qcow2" "$WORK/disk.qcow2" 8G
	boot_machine provision
	machine attach --qmp "$RUN/qmp.sock" --release "$MACHINE_RELEASE" --disk "$OUT/stage.img" --disk-format raw --disk-serial stage
	guest --timeout 600 -- 'for i in $(seq 30); do [ -e /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_stage ] && break; sleep 1; done;
		mkdir -p /mnt/stage && mount -o ro /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_stage /mnt/stage && sh /mnt/stage/provision.sh; rc=$?; umount /mnt/stage; exit $rc'
	stop_machine
	date >"$WORK/provisioned"
}

running() { [ -s "$RUN/machine.pid" ] && kill -0 "$(cat "$RUN/machine.pid")" 2>/dev/null; }

# sync_guest: this checkout's guest scripts replace the ones provision put in
# /opt/demo, so an edit reaches the running machine without a new disk.
sync_guest()
{
	local f script=""
	for f in "$here"/guest/*.sh "$here"/guest/*.py; do
		script+="echo $(base64 -w0 "$f") | base64 -d >/opt/demo/$(basename "$f")"$'\n'
	done
	guest --timeout 120 -- "$script"
}

# record NAME COMMAND...: run a guest script, keep its output in evidence/NAME.txt.
record()
{
	local name=$1 rc=0
	shift
	guest --timeout 1800 -- "$@" >"$EVIDENCE/$name.txt" 2>&1 || rc=$?
	cat "$EVIDENCE/$name.txt"
	return $rc
}

step_boot()
{
	if running; then
		say "the shirudo machine is already running"
		return
	fi
	mkdir -p "$WORK/tpm"
	APPEND=systemd.show_status=true boot_machine shirudo --lockdown shirudo --tpm "$WORK/tpm" --initrd "$OUT/initrd.img"
	perl -pe 's/\e\][^\e\a]*(\a|\e\\)//g; s/\e\[[0-9;?]*[A-Za-z]//g; s/\r//g' "$RUN/shirudo.console.log" >"$EVIDENCE/boot-console.log"
}

step_init()
{
	sync_guest
	say "shirudo init, in the initrd and again in the root file system"
	record init sh /opt/demo/init.sh || die "shirudo status is not ok; evidence/init.txt"
}

step_cve()
{
	sync_guest
	say "CVE-2025-24813: payload, load, payload, unload, payload"
	record cve sh /opt/demo/cve.sh || die "the payload did not exit 0, 10, 0; evidence/cve.txt"
	say "the registry's own tools/test, as its README runs it"
	record cve-tools-test "cd /opt/demo/registry && set -a && . /opt/demo/work/cve-2025-24813/env && SHIRUDO=/run/shirudo/runtime/usr/bin/shirudo tools/test /opt/demo/packages/cve-2025-24813-1.0.0.bee" ||
		die "tools/test failed; evidence/cve-tools-test.txt"
}

step_obs()
{
	sync_guest
	say "kernel-obs: what the kernel saw"
	record obs sh /opt/demo/obs.sh || die "kernel-obs failed; evidence/obs.txt"
}

step_stop()
{
	running || return 0
	say "stopping the machine"
	stop_machine
}

main()
{
	local s
	[ $# -gt 0 ] || set -- "${STEPS[@]}"
	[ "$1" = check ] || step_check
	for s in "$@"; do
		declare -F "step_$s" >/dev/null || die "no step $s; steps: ${STEPS[*]}"
		"step_$s"
	done
}

main "$@"
