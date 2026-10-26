# Runtime sources and licenses

The macOS runtime bundles these upstream components:

| Component | Version | License | Source |
| --- | --- | --- | --- |
| libkrun, including its Linux init | 1.19.7 | Apache-2.0 | https://github.com/libkrun/libkrun/tree/v1.19.7 |
| libkrunfw library | 5.6.2 | LGPL-2.1-only | https://github.com/libkrun/libkrunfw/tree/v5.6.2 |
| Kernel bundled by libkrunfw | 6.12.109 | GPL-2.0-only | https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-6.12.109.tar.xz |
| Alpine minirootfs (aarch64) | 3.22.2 | Multiple component licenses | https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/aarch64/ |
| gvproxy (Wolfcow deny-egress patch) | 0.8.9 | Apache-2.0 | https://github.com/containers/gvisor-tap-vsock/tree/v0.8.9 |

libkrunfw's tagged source contains the kernel patches, configuration, and build
instructions used for its kernel bundle. Alpine package sources are available
through https://gitlab.alpinelinux.org/alpine/aports and
https://distfiles.alpinelinux.org/distfiles/v3.22/; the image's APK database
records its installed package versions. The init also statically links Zig's
musl libc; sources and notices are in the pinned Zig 0.14.1 source distribution
at https://ziglang.org/download/0.14.1/zig-0.14.1.tar.xz.

`hack/runtime.env` pins downloaded build inputs by SHA-256. Cargo.lock in the
pinned libkrun source pins its Rust dependencies. Zig is a build tool and is
not included as an executable in the runtime archive.
The gvproxy source archive and its Go build toolchain are pinned by SHA-256 in
`hack/runtime.env`. `hack/patches/gvproxy-deny-egress.patch` disables direct
host TCP/UDP forwarding, upstream DNS, and the guest-facing mutable forwarding
API. It adds a per-run, exact-hostname TCP CONNECT policy. The patched binary
is built from vendored dependencies. Normal shell and offline one-shot runs do
not start it; network-enabled development runs do.

The exact Alpine package list and filesystem inventory are recorded in
[`images/alpine-minirootfs-3.22.2-aarch64.json`](images/alpine-minirootfs-3.22.2-aarch64.json).
The build regenerates this manifest from the pinned archive, checks it against
the committed copy, and bundles it as `runtime/rootfs.manifest.json`. The
archive is 3,981,043 bytes and has SHA-256
`6bf491907b705caa5fc65773fbbc1d0954530b6c455d4446f670a4c1fdcea489`.
The static `wolfcow-guest` executable is built separately and added to the
disposable rootfs before each boot. This image includes BusyBox and CA
certificates; it does not yet include Python, Bash, or Git.

Local packages are development builds, ad hoc signed and not notarized.
A published binary distribution must also provide the corresponding source
and license material required by its bundled components.
