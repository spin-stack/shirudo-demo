# shirudo-demo

[Shirudo](https://github.com/shirudo-dev/shirudo) loads signed BPF programs, called upatches, into a running Linux kernel. Each one blocks the code path an exploit takes until the real fix ships. This repository runs it in a [spin-machine](https://github.com/spin-stack/machine) guest. That is the virtual machine Spinbox gives a workspace, run here by hand without spin.

The demo is two programs: `demo` runs every step on the host, and `demo-guest` runs each step in the guest. Each release of this repository carries both, built static for linux/amd64, so a run needs no Go and no checkout. In an empty directory:

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

While the repository is private, `gh release download --repo spin-stack/shirudo-demo` fetches the same three files. The bundle says the tarball was built and signed by this repository's `release.yml` on `main`, as spin-stack/machine's `docs/releasing.md` ("Verify a release file") checks a spin-machine release.

`./demo run` downloads the [spin-machine](https://github.com/spin-stack/machine) release `versions.yaml` pins, v20261126.01, checks it, and boots it with the release's own `bin/spin-machine`: nothing of spin-machine is built. From an empty directory, the released `demo` with no Go on `PATH` took 777 s for `./demo check` and `./demo run` on this README's host (2026-11-26, 20 threads, KVM, other agents' machines running beside it), with shirudo's build container and the initrd's Ubuntu image already built by an earlier run; a first run also builds those two images. `evidence/demo-run.log` is that run.

`./demo run` runs the steps in order. Every step checks its own result and stops on a failure. `./demo STEP` runs one step, and expects the steps before it to have run. `./demo` with no step names them. It works in the directory it is run from: `work/` holds what it builds and `evidence/` what the guest printed. `WORK` and `EVIDENCE` put them elsewhere.

`versions.yaml` and `initrd/Dockerfile` are compiled into `demo`, and `demo-guest` is the file beside it: `demo` reads nothing of this repository as it runs.

### From a checkout

`go tool task` runs the same steps from a clone of this repository, with Go. Its `bin` task builds `demo` and `demo-guest` in `work/bin` as the release builds them, and every step runs that `demo`. A step whose result is already there is skipped, so the run can be repeated after a fix. `go tool task <step>` runs one step and what it needs; `go tool task --list` names them.

A release is `release.yml`, dispatched on `main` by a person. `go tool task release:build` builds its tarball, the workflow signs it, and go-tools' `task release` names it v<YYYYMMDD>.<NN> and publishes it once ci.yml's Gate has passed on the commit.

## What it shows

1. **A machine that can run shirudo.** A spin-machine release booted with `--lockdown shirudo` and a TPM. Shirudo's init runs in a systemd initrd, enrolls this run's signing certificate and seals the kernel's `.bpf` and `.dm-verity` keyrings. Then it runs again from the root file system and adopts what the initrd did. `shirudo status` reports `Status: ok` with every condition `OK`, and `Bound in: initrd`.
2. **A CVE blocked without patching the software.** [upatch.dev](https://upatch.dev)'s package for CVE-2025-24813 (Apache Tomcat partial PUT) is used as published, from [copper-helm/upatch-registry](https://github.com/copper-helm/upatch-registry). The registry ships source and no key, so its own `tools/build` builds the package and signs it with this run's key. The registry's payload exits 0 when Tomcat reaches the vulnerable path and 10 when it is blocked. The run shows 0, then 10 with the upatch loaded, then 0 after it is unloaded. The registry's `tools/test` then gives its own verdict on the same machine.
3. **What the kernel saw.** Shirudo's `kernel-obs` package records which programs ran, where connections went, which names resolved and who read credential files. The guest runs `id`, reads `/etc/shadow`, resolves upatch.dev and connects to Tomcat and to upatch.dev. `shirudo upatch map kernel-obs <table> --json` reads the records back in the guest, with no collector.

## What you need

- An x86_64 Linux host with KVM, where your user can open `/dev/kvm`. The release is linux/amd64 alone: the guest is an x86_64 KVM machine, and spin-machine is released for x86_64 hosts alone.
- Docker and git, and no Go. Shirudo builds in its own container (`testing/container` in its repository) and the initrd in an Ubuntu 26.04 container, so no compiler, clang, dracut or swtpm goes on the host. Docker stays until spin-machine's release carries shirudo itself (spin-stack/machine#759) and the guest image's systemd has TPM2 support, so the guest can make its own initrd. git clones shirudo and the registry at their pins.
- `cosign`, to check the release's bundle, and `curl` or `gh` to download it.
- About 15 GB of disk and 8 GB of memory. The guest gets 4 GiB (`MEMORY_MB`).
- Nothing of spin-machine. `./demo machine` downloads the release tarball `versions.yaml` pins into `work/machine`, checks its sha256 against the pin and its Sigstore bundle against the workflow that built it (`release.yml` at `refs/heads/main`, as spin-stack/machine's `docs/releasing.md` says, under the trust root go-tools pins), then unpacks it. The tarball carries QEMU, the kernel with shirudo's hooks, the base image, swtpm, passt and `bin/spin-machine` (spin-stack/machine#752).
- **A host that lets passt make its user namespace.** `spin-machine boot --net` gives the guest a NIC on passt, which sandboxes itself in a user namespace of its own (spin-stack/machine#751). `./demo check` runs the release's passt as boot runs it. Where the host's AppArmor refuses it (`kernel.apparmor_restrict_unprivileged_userns=1` on some Ubuntu images), check stops and prints the two commands that install passt's own profile, once, as root (spin-stack/machine#755, `docs/machine.md`, "A network without root"):

  ```sh
  work/machine/spin-machine-v20261126.01-linux-x86_64/usr/share/spin-stack/bin/spin-machine apparmor \
    --release "$PWD/work/machine/spin-machine-v20261126.01-linux-x86_64/usr/share/spin-stack" |
    sudo tee /etc/apparmor.d/spin-machine-passt >/dev/null
  sudo apparmor_parser -r /etc/apparmor.d/spin-machine-passt
  ```

  The profile applies to that release's passt at that path alone. `sudo apparmor_parser -R /etc/apparmor.d/spin-machine-passt` and removing the file take it away. The demo never changes the host itself.

`versions.yaml` pins the spin-machine release by sha256, the shirudo and upatch-registry commits, the Ubuntu image by digest, and Task. Shirudo's pin is the commit the release carries shirudo's kernel hooks from, its own `versions.yaml`'s `shirudo` pin at the release's tag, so the demo tests the shirudo the release ships hooks for. `./demo pins` reads that file and fails when the two differ. `./demo check` runs it, and so does CI. The release does not ship shirudo itself yet (spin-stack/machine#759), so the demo builds it at that pin.

## The steps

| Step | Where | What it does |
| --- | --- | --- |
| `machine` | host | Downloads the pinned spin-machine release, checks its sha256 and Sigstore bundle, and unpacks it in `work/machine`. |
| `pins` | host | Shirudo's pin is the one the release's `versions.yaml` names. `check` runs it; `run` does not run it alone. |
| `check` | host | `machine`, then: `demo-guest` is beside `demo`, the release has swtpm, passt and `bin/spin-machine`, `/dev/kvm` is usable, the tools are there, `pins`, and the release's passt can make its user namespace. |
| `shirudo` | container | Clones shirudo and the registry at their pins and builds shirudo (`make`, `make target shirudo-build`) against the release kernel's BTF. |
| `keys` | container | `shirudo genkey` makes this run's ML-DSA-87 operator key in `work/keys`. It signs the upatches, the config image and the runtime image. It is never committed, and a new run makes a new one. |
| `images` | container | The signed config image and the dm-verity runtime image, built as shirudo's [boot integration guide](https://github.com/shirudo-dev/shirudo/blob/5795f8d4a7509af2afeb331046f179300a1756a2/Documentation/operate/deploy/boot-integration.rst) builds them (`shirudo-gen-image`, `shirudo-gen-runtime`), and the initrd's bootstrap file that names the runtime image's root hash. |
| `initrd` | container | `shirudo-gen-initrd` runs dracut with shirudo's module, then checks the result with `--check`. |
| `packages` | container | The registry's `tools/build` builds and signs CVE-2025-24813's package. `shirudo bpm build` builds and signs `kernel-obs`. |
| `stage` | host | One ext4 image with what the guest gets: the images, the units, `demo-guest`, the packages, and the registry's payloads and tools. Every boot attaches it read-only and mounts it at `/opt/demo`. A stage built while the machine runs takes the old one's place before the next guest step, with no reboot. |
| `provision` | guest | A new 8 GiB qcow2 over the release's base image. The machine boots without shirudo and `demo-guest provision` grows the root file system, places the images under `/var/lib/shirudo/images`, installs `shirudo.service` with the runtime image's drop-in, and installs Python with apt, which the registry's payload is written in. |
| `boot` | host | `spin-machine boot --lockdown shirudo --tpm work/tpm --initrd work/out/initrd.img --net`. The console is a socket, and `demo` types commands at it. |
| `init` | guest | `demo-guest init` prints the kernel command line, lockdown, the TPM, the guest's network (`ip addr`, `ip route`, `/etc/resolv.conf`, `getent hosts upatch.dev`), both init runs from the journal, `shirudo status --show-full` and `shirudo image status`. |
| `cve` | guest | `demo-guest cve` runs the registry's `setup`, then payload, load, payload, unload, payload. Then `demo-guest cve-test` runs `tools/test`. |
| `obs` | guest | `demo-guest obs` loads `kernel-obs`, runs `id`, reads `/etc/shadow`, resolves upatch.dev, connects to Tomcat and to upatch.dev, and reads the tables. |
| `stop` | host | Powers the guest off. `work/disk.qcow2` and `work/tpm` stay, so `./demo boot` and `./demo init` boot the same machine again. Its initrd then reloads `kernel-obs` from shirudo's archive before the root file system starts. |

`./demo stop` and removing `work/` start over; from a checkout, `go tool task clean`.

## How the guest differs from a production target, and why

- **No measured boot.** spin-machine boots the kernel and the initrd through qboot (PVH), not a unified kernel image through systemd-stub. Nothing extends PCR 11 with the `enter-initrd` phase, and init says so: `tpm: PCR 11 holds no phase measurement; systemd measures enter-initrd only for a measured UKI boot, so a verifier cannot pin this binding to the initrd`. Everything else runs, and `status` is `ok`. A verifier of this machine's attestation cannot tell an initrd binding from a later one (spin-stack/machine#742).
- **The initrd is built in a container.** The guest image's systemd is rebuilt without TPM2 support, so dracut in the guest could not add `systemd-pcrphase-initrd.service`, which `shirudo-gen-initrd --check` requires. `initrd/Dockerfile` uses Ubuntu 26.04's own systemd 259 and dracut 110. The initrd's systemd hands over to the guest's at switch-root.
- **The network is passt's.** `--net` gives the guest a NIC on passt, as the user who runs the demo: the guest reaches what that user reaches, IPv4 only, with DNS through the host's resolver. A workspace in Spinbox gets a TAP from spin's runner instead. The registry's `setup` installs Java with apt and downloads Tomcat 9.0.98 from archive.apache.org, checking it against the `.sha512` published beside it, unchanged. Nothing stands in for the network: no local resolver, no staged packages, no stand-in `curl`.
- **The stage disk carries what only this run has.** The registry ships source and no key, so its CVE package and `kernel-obs` are built and signed on the host with this run's key, and reach the guest on the stage disk with the registry's `payloads/` and `tools/` from the same commit. The guest has no compiler or key to build them itself.
- **The root file system is grown by ioctl.** The base image's file system is 1 GiB with about 100 MiB free, too small for Java. `resize2fs` cannot grow it in the guest: it opens `/dev/vda` for writing, and this kernel refuses that for a mounted device (`CONFIG_BLK_DEV_WRITE_MOUNTED` is off, as shirudo's hardening asks). `demo-guest provision` calls ext4's online resize, `EXT4_IOC_RESIZE_FS` on `/`.
- **The console logs in as root.** The machine boots with `systemd.set_credential=agetty.autologin:root`, as `spin-machine-dev shell` does, so the host has a shell to type at. The base image has no such login.
- **One key for everything.** The operator key signs the upatches, the config image and the runtime image. Shirudo allows one key for all three. A deployment keeps them apart.

## Then, in Spinbox

The same steps as a `.spin.yaml`: the `create` task sets up Tomcat and loads the upatches, and a health check runs the payload. They run with the `spin` CLI against a Spinbox installation, where the workspace's log shows them. This needs a spin release that runs shirudo's init in the guest's initrd (spin-stack/spin#1708) and a decision on spin's own BPF under shirudo (spin-stack/spin#1709).
