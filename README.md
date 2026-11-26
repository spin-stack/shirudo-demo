# shirudo-demo

[Shirudo](https://github.com/shirudo-dev/shirudo) loads signed BPF programs, called upatches, into a running Linux kernel. Each one blocks the code path an exploit takes until the real fix ships. This repository runs it in a [spin-machine](https://github.com/spin-stack/machine) guest. That is the virtual machine Spinbox gives a workspace, run here by hand without spin.

One command does everything on one Linux host, and installs nothing in the host's system:

```sh
MACHINE_RELEASE=~/spin-machine/usr/share/spin-stack SPIN_MACHINE=~/machine/_output/bin/spin-machine go tool task
```

`Taskfile.yml` runs the steps in order. Every step checks its own result and stops on a failure. A step whose result is already there is skipped, so the run can be repeated after a fix. `go tool task <step>` runs one step and what it needs; `go tool task --list` names them. What the guest printed for each step lands in `evidence/`.

`cmd/demo` does each step on the host. `cmd/demo-guest` does each step in the guest: a static binary on the stage disk, which the guest mounts at `/opt/demo`.

## What it shows

1. **A machine that can run shirudo.** A spin-machine release booted with `--lockdown shirudo` and a TPM. Shirudo's init runs in a systemd initrd, enrolls this run's signing certificate and seals the kernel's `.bpf` and `.dm-verity` keyrings. Then it runs again from the root file system and adopts what the initrd did. `shirudo status` reports `Status: ok` with every condition `OK`, and `Bound in: initrd`.
2. **A CVE blocked without patching the software.** [upatch.dev](https://upatch.dev)'s package for CVE-2025-24813 (Apache Tomcat partial PUT) is used as published, from [copper-helm/upatch-registry](https://github.com/copper-helm/upatch-registry). The registry ships source and no key, so its own `tools/build` builds the package and signs it with this run's key. The registry's payload exits 0 when Tomcat reaches the vulnerable path and 10 when it is blocked. The run shows 0, then 10 with the upatch loaded, then 0 after it is unloaded. The registry's `tools/test` then gives its own verdict on the same machine.
3. **What the kernel saw.** Shirudo's `kernel-obs` package records which programs ran, where connections went, which names resolved and who read credential files. The guest runs `id`, reads `/etc/shadow`, resolves upatch.dev and connects to Tomcat and to upatch.dev. `shirudo upatch map kernel-obs <table> --json` reads the records back in the guest, with no collector.

## What you need

- An x86_64 Linux host with KVM, where your user can open `/dev/kvm`.
- Go, Docker and git. `go tool task` fetches the Task that `versions.yaml` pins. Shirudo builds in its own container (`testing/container` in its repository) and the initrd in an Ubuntu 26.04 container, so no compiler, clang, dracut or swtpm goes on the host.
- About 15 GB of disk and 8 GB of memory. The guest gets 4 GiB (`MEMORY_MB`).
- **A spin-machine release from after 2026-11-26.** The first release cut after that date is the first to carry everything this needs: the 7.4 kernel with shirudo's hooks and configuration (spin-stack/machine#733, #735, #744), the lockdown floor and `--lockdown shirudo` (#743), a TPM per machine with swtpm in the release (#747, #748), and a network on passt, `--net` (#751). v20261124.01 and older have no TPM and no passt, and `go tool task check` says so.
  - Unpack the release's tarball: `mkdir ~/spin-machine && tar -xzf spin-machine-<version>-linux-x86_64.tar.gz -C ~/spin-machine`. `MACHINE_RELEASE` is then `~/spin-machine/usr/share/spin-stack`.
  - The tarball ships no `spin-machine` command. Build it from the same tag, with Go: `git clone https://github.com/spin-stack/machine ~/machine && cd ~/machine && git checkout <version> && go run ./cmd/spin-machine-dev build spin-machine`. `SPIN_MACHINE` is then `~/machine/_output/bin/spin-machine`.
  - Until that release exists, a tree built from machine's `main` does the same: `go run ./cmd/spin-machine-dev build` in the clone, then `MACHINE_RELEASE=~/machine/_output`, which holds `bin/spin-machine` too.

`versions.yaml` pins the shirudo and upatch-registry commits, the Ubuntu image by digest, and Task.

## The steps

| Step | Where | What it does |
| --- | --- | --- |
| `check` | host | The release tree has a TPM and passt, `/dev/kvm` is usable, the tools are there. |
| `shirudo` | container | Clones shirudo and the registry at their pins and builds shirudo (`make`, `make target shirudo-build`) against the release kernel's BTF. |
| `keys` | container | `shirudo genkey` makes this run's ML-DSA-87 operator key in `work/keys`. It signs the upatches, the config image and the runtime image. It is never committed, and a new run makes a new one. |
| `images` | container | The signed config image and the dm-verity runtime image, built as shirudo's [boot integration guide](https://github.com/shirudo-dev/shirudo/blob/2a560729814a9ac0ac702bb15d39c2145522c143/Documentation/operate/deploy/boot-integration.rst) builds them (`shirudo-gen-image`, `shirudo-gen-runtime`), and the initrd's bootstrap file that names the runtime image's root hash. |
| `initrd` | container | `shirudo-gen-initrd` runs dracut with shirudo's module, then checks the result with `--check`. |
| `packages` | container | The registry's `tools/build` builds and signs CVE-2025-24813's package. `shirudo bpm build` builds and signs `kernel-obs`. |
| `guest` | host | `cmd/demo-guest`, built static for linux/amd64. |
| `stage` | host | One ext4 image with what the guest gets: the images, the units, `demo-guest`, the packages, and the registry's payloads and tools. Every boot attaches it read-only and mounts it at `/opt/demo`. A stage built while the machine runs takes the old one's place before the next guest step, with no reboot. |
| `provision` | guest | A new 8 GiB qcow2 over the release's base image. The machine boots without shirudo and `demo-guest provision` grows the root file system, places the images under `/var/lib/shirudo/images`, installs `shirudo.service` with the runtime image's drop-in, and installs Python with apt, which the registry's payload is written in. |
| `boot` | host | `spin-machine boot --lockdown shirudo --tpm work/tpm --initrd work/out/initrd.img --net`. The console is a socket, and `internal/console` types commands at it. |
| `init` | guest | `demo-guest init` prints the kernel command line, lockdown, both init runs from the journal, `shirudo status --show-full` and `shirudo image status`. |
| `cve` | guest | `demo-guest cve` runs the registry's `setup`, then payload, load, payload, unload, payload. Then `demo-guest cve-test` runs `tools/test`. |
| `obs` | guest | `demo-guest obs` loads `kernel-obs`, runs `id`, reads `/etc/shadow`, resolves upatch.dev, connects to Tomcat and to upatch.dev, and reads the tables. |
| `stop` | host | Powers the guest off. `work/disk.qcow2` and `work/tpm` stay, so `go tool task boot init` boots the same machine again. Its initrd then reloads `kernel-obs` from shirudo's archive before the root file system starts. |

`go tool task clean` starts over. `WORK` puts `work/` elsewhere.

## How the guest differs from a production target, and why

- **No measured boot.** spin-machine boots the kernel and the initrd through qboot (PVH), not a unified kernel image through systemd-stub. Nothing extends PCR 11 with the `enter-initrd` phase, and init says so: `tpm: PCR 11 holds no phase measurement; systemd measures enter-initrd only for a measured UKI boot, so a verifier cannot pin this binding to the initrd`. Everything else runs, and `status` is `ok`. A verifier of this machine's attestation cannot tell an initrd binding from a later one (spin-stack/machine#742).
- **The initrd is built in a container.** The guest image's systemd is rebuilt without TPM2 support, so dracut in the guest could not add `systemd-pcrphase-initrd.service`, which `shirudo-gen-initrd --check` requires. `initrd/Dockerfile` uses Ubuntu 26.04's own systemd 259 and dracut 110. The initrd's systemd hands over to the guest's at switch-root.
- **The network is passt's.** `--net` gives the guest a NIC on passt, as the user who runs the demo: the guest reaches what that user reaches, IPv4 only, with DNS through the host's resolver. A workspace in Spinbox gets a TAP from spin's runner instead. The payload's `setup` downloads Tomcat and installs Java with apt, unchanged.
- **The root file system is grown by ioctl.** The base image's file system is 1 GiB with about 100 MiB free, too small for Java. `resize2fs` cannot grow it in the guest: it opens `/dev/vda` for writing, and this kernel refuses that for a mounted device (`CONFIG_BLK_DEV_WRITE_MOUNTED` is off, as shirudo's hardening asks). `demo-guest provision` calls ext4's online resize, `EXT4_IOC_RESIZE_FS` on `/`.
- **The console logs in as root.** The machine boots with `systemd.set_credential=agetty.autologin:root`, as `spin-machine-dev shell` does, so the host has a shell to type at. The base image has no such login.
- **One key for everything.** The operator key signs the upatches, the config image and the runtime image. Shirudo allows one key for all three. A deployment keeps them apart.

## Then, in Spinbox

The same steps as a `.spin.yaml`: the `create` task sets up Tomcat and loads the upatches, and a health check runs the payload. They run with the `spin` CLI against a Spinbox installation, where the workspace's log shows them. This needs a spin release that runs shirudo's init in the guest's initrd (spin-stack/spin#1708) and a decision on spin's own BPF under shirudo (spin-stack/spin#1709).
