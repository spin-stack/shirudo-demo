# shirudo-demo

**Not runnable yet.** This is the plan. It turns into commands once shirudo has run in a spin-machine guest. Until then, spin-stack/machine#719 (the guest's TPM) and #740 are open.

[Shirudo](https://github.com/shirudo-dev/shirudo) loads signed BPF programs, called upatches, into a running Linux kernel. Each one blocks the code path an exploit takes until the real fix ships. This repository runs it in a [spin-machine](https://github.com/spin-stack/machine) guest. That is the virtual machine Spinbox gives a workspace, run here by hand without spin.

## What it shows

1. **A machine that can run shirudo.** A spin-machine release booted with `--lockdown shirudo` and a virtual TPM. Shirudo's init runs in its systemd initrd, enrolls the signing key and seals the kernel's `.bpf` keyring.
2. **A CVE blocked without patching the software.** [upatch.dev](https://upatch.dev)'s package for CVE-2025-24813 (Apache Tomcat partial PUT) is used as published, from [copper-helm/upatch-registry](https://github.com/copper-helm/upatch-registry). It is built and signed with this demo's own key, because the registry ships source and no key. The registry's payload exits 0 when Tomcat reaches the vulnerable path and 10 when it is blocked. The sequence is: payload 0, load the upatch, payload 10, unload, payload 0.
3. **What the kernel saw.** Shirudo's `kernel-obs` package records which programs ran, where connections went, which names resolved and who read credential files. `shirudo upatch map kernel-obs` reads it in the guest, with no collector needed.

## Then, in Spinbox

The same steps as a `.spin.yaml`: the `create` task sets up Tomcat and loads the upatches, and a health check runs the payload. They run with `spin` CLI against a Spinbox installation, where the workspace's log shows them. This needs a spin release that runs shirudo's init in the guest's initrd (spin-stack/spin#1708) and a decision on spin's own BPF under shirudo (spin-stack/spin#1709).
