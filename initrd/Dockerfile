# The systemd initrd shirudo's init runs in, built by dracut from Ubuntu 26.04's
# own systemd: the guest image's systemd is rebuilt without TPM2 support, so the
# initrd cannot be made from it (README, "How the guest differs").
ARG UBUNTU_IMAGE
FROM ${UBUNTU_IMAGE}
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        dracut-core systemd udev kmod tpm2-tools libtss2-tcti-device0 \
        cpio zstd file e2fsprogs util-linux mount \
    && rm -rf /var/lib/apt/lists/*
