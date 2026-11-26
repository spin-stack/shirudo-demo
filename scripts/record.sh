#!/bin/bash
# shellcheck disable=SC2016,SC2329 # the steps are typed as text and run with eval
# Records the README's run from an empty directory, as a reader types it, with
# asciinema: scripts/record.sh [DIR [CAST]]. DIR (/var/tmp/shirudo-run) must not
# exist or be empty; it is outside $HOME so no path in the recording names you.
# CAST defaults to evidence/demo-run.cast. Needs gh, asciinema, KVM, and cosign
# or Docker (cosign's image stands in for it).
set -euo pipefail

if [ "${1:-}" = --steps ]; then
	cd "$2"
	unset WORK EVIDENCE
	if ! command -v cosign >/dev/null; then
		cosign() { docker run --rm -v "$PWD:$PWD" -w "$PWD" ghcr.io/sigstore/cosign/cosign:v2.6.1 "$@"; }
	fi
	step() {
		printf '\033[1;32m$\033[0m %s\n' "$*"
		sleep 0.6
		eval "$*"
	}
	step "gh release download --repo spin-stack/shirudo-demo --pattern '*'"
	step 'sha256sum -c checksums.txt'
	step 'cosign verify-blob --bundle shirudo-demo_linux_amd64.tar.gz.sigstore.json --certificate-identity https://github.com/spin-stack/shirudo-demo/.github/workflows/release.yml@refs/heads/main --certificate-oidc-issuer https://token.actions.githubusercontent.com shirudo-demo_linux_amd64.tar.gz'
	step 'tar -xzf shirudo-demo_linux_amd64.tar.gz && cd shirudo-demo'
	step 'ls'
	step './demo check'
	step './demo run'
	step './demo boot'
	step 'pstree -p -T $(cut -d" " -f1 work/run/machine)'
	step './demo stop'
	exit
fi

repo=$(cd "$(dirname "$0")/.." && pwd)
dir=${1:-/var/tmp/shirudo-run}
cast=$(realpath -m "${2:-$repo/evidence/demo-run.cast}")
mkdir -p "$dir"
if [ -n "$(ls -A "$dir")" ]; then
	echo "$dir is not empty: ./demo stop in it, then rm -rf it" >&2
	exit 1
fi
cp "$0" "$dir/.record.sh"
cd "$dir"
asciinema rec --headless --window-size 120x34 --idle-time-limit 2 --overwrite \
	--title "shirudo-demo from an empty directory" \
	-c "bash .record.sh --steps $dir" "$cast"
rm -f "$dir/.record.sh"
