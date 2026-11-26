# shirudo-demo

[Shirudo](https://github.com/shirudo-dev/shirudo) loads signed BPF programs (upatches) into a running kernel, blocking the code path an exploit takes until the real fix ships. This demo runs it in a [spin-machine](https://github.com/spin-stack/machine) guest, the VM Spinbox gives a workspace, by hand and without spin.

Everything comes from the pinned spin-machine release: the machine, the kernel with shirudo's hooks, the shirudo the guest runs, and the operator tools that make what it boots with. Nothing is built, and nothing runs in Docker.

## Run it

Each release carries `demo` (host) and `demo-guest` (guest), static for linux/amd64. In an empty directory:

```sh
base=https://github.com/spin-stack/shirudo-demo/releases/latest/download
for f in shirudo-demo_linux_amd64.tar.gz shirudo-demo_linux_amd64.tar.gz.sigstore.json checksums.txt; do
  curl -fsSLO "$base/$f"
done
sha256sum -c checksums.txt
cosign verify-blob --bundle shirudo-demo_linux_amd64.tar.gz.sigstore.json \
  --certificate-identity https://github.com/spin-stack/shirudo-demo/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com shirudo-demo_linux_amd64.tar.gz
tar -xzf shirudo-demo_linux_amd64.tar.gz && cd shirudo-demo
./demo check
./demo run
```

While the repository is private, `gh release download --repo spin-stack/shirudo-demo --pattern '*'` fetches the same files.

`./demo run` runs every step in order and stops at the first failure; `./demo STEP` runs one. `work/` holds what it builds and `evidence/` what the guest printed (`WORK`, `EVIDENCE` move them). `./demo stop` and `rm -rf work` start over.

From a checkout, `go tool task` runs the same steps with Go, skipping those whose results are already there; `go tool task clean` starts over.

## What it shows

1. **A machine that runs shirudo.** Booted with `--lockdown shirudo` and a TPM, shirudo's init runs in the initrd, enrolls this run's certificate and seals the `.bpf` and `.dm-verity` keyrings, then runs again from the root file system. `shirudo status` reports `ok`, bound in the initrd.
2. **A CVE blocked without patching.** [upatch.dev](https://upatch.dev)'s CVE-2025-24813 package (Tomcat partial PUT), built from [copper-helm/upatch-registry](https://github.com/copper-helm/upatch-registry) with its own `tools/build` and signed with this run's key. The registry's payload exits 0, then 10 with the upatch loaded, then 0 after unload; its `tools/test` passes.
3. **What the kernel saw.** Shirudo's `kernel-obs` records programs run, connections, DNS names and credential-file reads, read back in the guest with `shirudo upatch map`.

## What you need

- An x86_64 Linux host with KVM your user can open.
- git (the registry is cloned at its pin), `cosign`, and `curl` or `gh`. No Go, no Docker.
- About 15 GB of disk and 8 GB of memory; the guest gets 4 GiB (`MEMORY_MB`).
- A host where passt can make its user namespace. Where AppArmor refuses it, `./demo check` stops and prints the two commands that install passt's own profile, once, as root:

  ```sh
  work/machine/spin-machine-v20261126.04-linux-x86_64/usr/share/spin-stack/bin/spin-machine apparmor \
    --release "$PWD/work/machine/spin-machine-v20261126.04-linux-x86_64/usr/share/spin-stack" |
    sudo tee /etc/apparmor.d/spin-machine-passt >/dev/null
  sudo apparmor_parser -r /etc/apparmor.d/spin-machine-passt
  ```

`versions.yaml` pins the release's two tarballs by sha256, the registry commit and Task. It is compiled into `demo`.

## The steps

| Step | Where | What it does |
| --- | --- | --- |
| `machine` | host | Downloads the release and its `shirudo-operator` tarball, checks each sha256 and Sigstore bundle, unpacks them in `work/machine`. |
| `check` | host | The release is complete, `/dev/kvm` is usable, git is there, passt can sandbox itself. |
| `operator` | guest | A throwaway guest runs the release's operator half: key, signed config and dm-verity runtime images, the CVE and `kernel-obs` packages, units and the initrd. The host reads them back into `work/out`. |
| `stage` | host | An ext4 image with what the shirudo machine needs, mounted read-only at `/opt/demo`. |
| `provision` | guest | A new disk over the base image; installs shirudo's images, unit and Python. |
| `boot` | host | `spin-machine boot --lockdown shirudo --tpm --initrd --net`, console on a socket. |
| `init` | guest | Command line, lockdown, TPM, network, both init runs, `shirudo status`. |
| `cve` | guest | The registry's setup, then payload / load / payload / unload / payload, then `tools/test`. |
| `obs` | guest | Loads `kernel-obs`, makes the kernel do five things, reads the tables. |
| `stop` | host | Powers off; `work/disk.qcow2` and `work/tpm` stay for the next boot. |

## How it differs from production

- **The operator is a throwaway guest of the same release**, as the release's own check runs it, not a separate signing machine.
- **One key** signs the upatches and both images. A deployment keeps them apart.
- **The network is passt's**: IPv4, as the user running the demo. Spinbox gives a TAP instead.
- **The console logs in as root**, so the host has a shell to type at.

## Then, in Spinbox

The same steps as a `.spin.yaml`, once spin runs shirudo's init in the guest's initrd (spin-stack/spin#1708) and decides on its own BPF under shirudo (spin-stack/spin#1709).
