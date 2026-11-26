# shirudo-demo

[Shirudo](https://github.com/shirudo-dev/shirudo) loads signed BPF programs, called upatches, into a running Linux kernel. Each one blocks the code path an exploit takes until the real fix ships. This repository runs it in a [spin-machine](https://github.com/spin-stack/machine) guest. That is the virtual machine Spinbox gives a workspace, run here by hand without spin.

One script does everything on one Linux host, and installs nothing in the host's system:

```sh
MACHINE_RELEASE=~/spin-machine/usr/share/spin-stack SPIN_MACHINE=~/machine/_output/bin/spin-machine ./demo.sh
```

Every step checks its own result and stops on a failure. A step that has already run is skipped, so the script can be run again after a fix. `./demo.sh <step>...` runs only the named steps. What the guest printed for each step lands in `evidence/`.

## What it shows

1. **A machine that can run shirudo.** A spin-machine release booted with `--lockdown shirudo` and a TPM. Shirudo's init runs in a systemd initrd, enrolls this run's signing certificate and seals the kernel's `.bpf` and `.dm-verity` keyrings. Then it runs again from the root file system and adopts what the initrd did. `shirudo status` reports `Status: ok` with every condition `OK`, and `Bound in: initrd`.
2. **A CVE blocked without patching the software.** [upatch.dev](https://upatch.dev)'s package for CVE-2025-24813 (Apache Tomcat partial PUT) is used as published, from [copper-helm/upatch-registry](https://github.com/copper-helm/upatch-registry). The registry ships source and no key, so its own `tools/build` builds the package and signs it with this run's key. The registry's payload exits 0 when Tomcat reaches the vulnerable path and 10 when it is blocked. The run shows 0, then 10 with the upatch loaded, then 0 after it is unloaded. The registry's `tools/test` then gives its own verdict on the same machine.
3. **What the kernel saw.** Shirudo's `kernel-obs` package records which programs ran, where connections went, which names resolved and who read credential files. The guest runs `id`, reads `/etc/shadow`, resolves a name and connects to Tomcat. `shirudo upatch map kernel-obs <table> --json` reads the records back in the guest, with no collector.

## What you need

- An x86_64 Linux host with KVM, where your user can open `/dev/kvm`.
- Docker, git, python3, curl and binutils (`strings`). Shirudo builds in its own container (`testing/container` in its repository) and the initrd in an Ubuntu 26.04 container, so no compiler, clang, dracut or swtpm goes on the host.
- About 15 GB of disk and 8 GB of memory. The guest gets 4 GiB (`MEMORY_MB`).
- **A spin-machine release from after 2026-11-26.** The first release cut after that date is the first to carry everything this needs: the 7.4 kernel with shirudo's hooks and configuration (spin-stack/machine#733, #735, #744), the lockdown floor and `--lockdown shirudo` (#743), and a TPM per machine with swtpm in the release (#747, #748). v20261124.01 and older have no TPM, and `./demo.sh` says so.
  - Unpack the release's tarball: `mkdir ~/spin-machine && tar -xzf spin-machine-<version>-linux-x86_64.tar.gz -C ~/spin-machine`. `MACHINE_RELEASE` is then `~/spin-machine/usr/share/spin-stack`.
  - The tarball ships no `spin-machine` command. Build it from the same tag, with Go: `git clone https://github.com/spin-stack/machine ~/machine && cd ~/machine && git checkout <version> && go run ./cmd/spin-machine-dev build spin-machine`. `SPIN_MACHINE` is then `~/machine/_output/bin/spin-machine`.
  - Until that release exists, a tree built from machine's `main` does the same: `go run ./cmd/spin-machine-dev build` in the clone, then `MACHINE_RELEASE=~/machine/_output`, which holds `bin/spin-machine` too.

`versions.env` pins the shirudo and upatch-registry commits, and the Ubuntu image by digest.

## The steps

| Step | Where | What it does |
| --- | --- | --- |
| `check` | host | The release tree has a TPM, `/dev/kvm` is usable, the tools are there. |
| `shirudo` | container | Clones shirudo and the registry at their pins and builds shirudo (`make`, `make target shirudo-build`) against the release kernel's BTF. |
| `keys` | container | `shirudo genkey` makes this run's ML-DSA-87 operator key in `work/keys`. It signs the upatches, the config image and the runtime image. It is never committed, and a new run makes a new one. |
| `images` | container | The signed config image and the dm-verity runtime image, built as shirudo's [boot integration guide](https://github.com/shirudo-dev/shirudo/blob/2a560729814a9ac0ac702bb15d39c2145522c143/Documentation/operate/deploy/boot-integration.rst) builds them (`shirudo-gen-image`, `shirudo-gen-runtime`), and the initrd's bootstrap file that names the runtime image's root hash. |
| `initrd` | container | `shirudo-gen-initrd` runs dracut with shirudo's module, then checks the result with `--check`. |
| `packages` | container | The registry's `tools/build` builds and signs CVE-2025-24813's package. `shirudo bpm build` builds and signs `kernel-obs`. |
| `stage` | host | One ext4 image with what the guest gets: the images, the units, the packages, the registry's payloads, Tomcat's tarball and the Ubuntu packages of Java and Python. |
| `provision` | guest | A new 8 GiB qcow2 over the release's base image. The machine boots without shirudo, the stage disk is attached, and `guest/provision.sh` grows the root file system, places the images under `/var/lib/shirudo/images`, installs `shirudo.service` with the runtime image's drop-in, and installs Java and Python. |
| `boot` | host | `spin-machine boot --lockdown shirudo --tpm work/tpm --initrd work/out/initrd.img`. The console is a socket, and `lib/guest.py` types commands at it. |
| `init` | guest | `guest/init.sh` prints the kernel command line, lockdown, both init runs from the journal, `shirudo status`, `shirudo image status` and the keyrings. |
| `cve` | guest | `guest/cve.sh` runs the registry's `setup`, then payload, load, payload, unload, payload. Then `tools/test`. |
| `obs` | guest | `guest/obs.sh` loads `kernel-obs`, does the four things and reads the tables. |
| `stop` | host | Powers the guest off. `work/disk.qcow2` and `work/tpm` stay, so `./demo.sh boot init` boots the same machine again. Its initrd then reloads `kernel-obs` from shirudo's archive before the root file system starts. |

`rm -rf work` starts over.

## How the guest differs from a production target, and why

- **No measured boot.** spin-machine boots the kernel and the initrd through qboot (PVH), not a unified kernel image through systemd-stub. Nothing extends PCR 11 with the `enter-initrd` phase, and init says so: `tpm: PCR 11 holds no phase measurement; systemd measures enter-initrd only for a measured UKI boot, so a verifier cannot pin this binding to the initrd`. Everything else runs, and `status` is `ok`. A verifier of this machine's attestation cannot tell an initrd binding from a later one (spin-stack/machine#742).
- **The initrd is built in a container.** The guest image's systemd is rebuilt without TPM2 support, so dracut in the guest could not add `systemd-pcrphase-initrd.service`, which `shirudo-gen-initrd --check` requires. `initrd/Dockerfile` uses Ubuntu 26.04's own systemd 259 and dracut 110. The initrd's systemd hands over to the guest's at switch-root.
- **No network.** `spin-machine boot` gives the guest no network interface. The payload's `setup` runs as published, but it downloads Tomcat from archive.apache.org and installs Java with apt. The host fetches the tarball, and `guest/offline/curl`, first on `setup`'s PATH, answers those two URLs from the staged files. `setup` still checks the tarball's SHA-512. Java is already installed when `setup` runs, from the Ubuntu package apt would have installed, so `setup` skips apt. The DNS lookup goes to a resolver on 127.0.0.1:53 that `guest/dns.py` starts for that one question.
- **The root file system is grown by ioctl.** The base image's file system is 1 GiB with about 100 MiB free, too small for Java. `resize2fs` cannot grow it in the guest: it opens `/dev/vda` for writing, and this kernel refuses that for a mounted device (`CONFIG_BLK_DEV_WRITE_MOUNTED` is off, as shirudo's hardening asks). `guest/provision.sh` calls ext4's online resize, `EXT4_IOC_RESIZE_FS` on `/`, from perl.
- **The console logs in as root.** The machine boots with `systemd.set_credential=agetty.autologin:root`, as `spin-machine-dev shell` does, so the script has a shell to type at. The base image has no such login.
- **One key for everything.** The operator key signs the upatches, the config image and the runtime image. Shirudo allows one key for all three. A deployment keeps them apart.

## Then, in Spinbox

The same steps as a `.spin.yaml`: the `create` task sets up Tomcat and loads the upatches, and a health check runs the payload. They run with the `spin` CLI against a Spinbox installation, where the workspace's log shows them. This needs a spin release that runs shirudo's init in the guest's initrd (spin-stack/spin#1708) and a decision on spin's own BPF under shirudo (spin-stack/spin#1709).
