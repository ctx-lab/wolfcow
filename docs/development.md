# Development

The current build target is Apple Silicon macOS. Building from source requires
Go 1.21 or later, [Task](https://taskfile.dev), Rust/Cargo, Apple command line
tools, and internet access. `task build` verifies and downloads pinned sources,
builds the native runtime, and signs the local helper with the Hypervisor
entitlement. End users of a prepared archive do not need these build tools.

```sh
task build           # CLI, libkrun, firmware, and default Linux image
task verify          # Go tests, vet, and static Linux guest build
task package         # self-contained macOS archive in dist/
./bin/wolfcow shell     # boot a disposable shell
task stage:a         # experimental guest ready + exec over vsock
```

The default rootfs is recreated for every run and removed on exit. An optional
`wolfcow shell ROOTFS` (or `--rootfs ROOTFS`, or `WOLFCOW_ROOTFS=ROOTFS`) copies a trusted
ARM64 Linux rootfs before boot. Guest changes are discarded. Never use a host
project or home directory as a source. The CLI uses Cobra for command parsing;
the optional rootfs source can also be set with `WOLFCOW_ROOTFS`.

The Go module is `github.com/ctx-lab/wolfcow`. `internal/vmm` owns VM lifecycle
and image staging; `internal/protocol` defines the bounded guest stream.
`cmd/wolfcow-guest` builds a static Linux guest agent. `internal/cli` owns the
command surface. `task stage:a` uses a hidden development command and requires
a macOS terminal that permits local Unix sockets and the Hypervisor framework.
Native C interop lives in `native/`; reproducible build and probe scripts live
in `hack/`. The backend interface and guest protocol are experimental; egress
and session packages will be added as those capabilities are implemented.

## App Sandbox probe

`task security:app-sandbox` signs separate test executables and checks whether
macOS App Sandbox lets the helper read a system fixture while denying a private
file. It never changes the shipped helper. The first host run failed before
`main`: its crash report said the signed executable lacked an embedded
`Info.plist` bundle identifier. The probe now embeds matching identifiers. In
the current development environment, the minimal signed process still aborts
before libkrun runs, so App Sandbox confinement remains unverified.
`WOLFCOW_SIGN_IDENTITY` can select an installed signing identity;
otherwise the probe signs ad hoc. The probe's temporary library-validation
exception is absent from the product helper.

The probe is only a file-access experiment. A passing result must be followed
by a VM boot under App Sandbox and negative host-path tests before making any
confinement claim.
