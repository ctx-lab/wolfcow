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
task stage:b         # copied workspace smoke and guest protocol conformance
task image:smoke     # boot the pinned image and check shell and CA bundle
task net:link        # experimental virtio-net/gvproxy local connectivity
task net:external    # optional external TCP CONNECT smoke on physical macOS
task net:checks      # run both network probes after one build
```

The default rootfs is recreated for every run and removed on exit. An optional
`wolfcow shell ROOTFS` (or `--rootfs ROOTFS`, or `WOLFCOW_ROOTFS=ROOTFS`) copies a trusted
ARM64 Linux rootfs before boot. Guest changes are discarded. Never use a host
project or home directory as a source. The CLI uses Cobra for command parsing;
the optional rootfs source can also be set with `WOLFCOW_ROOTFS`.

The default Alpine minirootfs is pinned by archive digest in
`hack/runtime.env`. `task build` regenerates its package and file inventory,
compares it with `images/alpine-minirootfs-3.22.2-aarch64.json`, and includes
the manifest in the runtime archive. `task image:smoke` checks that the image
boots with BusyBox and the CA bundle. The image does not include Python, Bash,
or Git.

Runs attach no network device unless a per-run `internal/network.Policy`
allows at least one exact DNS hostname and TCP port pair; the hidden
`spike workspace --allow-tcp HOST:PORT` flag sets one. The bundled gvproxy is
built from pinned v0.8.9 source with `hack/patches/gvproxy-deny-egress.patch`,
using a pinned Go 1.25 toolchain fetched by the build. With that patch:

- unknown DNS names get NXDOMAIN, direct guest TCP and UDP forwarding is
  disabled, and the guest cannot call gvproxy's forwarding API;
- the guest reaches the host proxy through HTTP(S) proxy environment
  variables, and the proxy accepts CONNECT only for an allowed pair;
- the proxy resolves the name once, rejects private, local, and selected
  special-purpose IPv4 results, and dials the selected IP without a second
  lookup;
- tunnels carry opaque TCP bytes, close after two minutes without traffic, and
  forward half-closes where the transport supports them; and
- the guest client validates TLS. Inside an allowed tunnel the guest controls
  TLS SNI and HTTP `Host`, including on shared CDN addresses.

IPv6, credential substitution, and a public network CLI are not implemented.

The network probes run on physical Apple Silicon macOS:

- `task net:link` uses BusyBox `wget` to reach the gateway health endpoint and
  a static synthetic endpoint, and checks denied hostnames and raw-IP requests.
- `task net:external` sends an HTTP request through a CONNECT tunnel to
  `example.com:80` and requires prompt denial of blocked hostnames, hostname
  suffixes, other ports, raw-IP CONNECT, and direct raw-IP traffic. Its output
  includes the workspace fixture diff from the development command it uses.
- `task net:checks` runs both after one build.

These probes do not validate TLS and are not a complete negative egress suite.

The Go module is `github.com/ctx-lab/wolfcow`. `internal/vmm` owns VM lifecycle
and image staging; `internal/protocol` defines the bounded guest stream.
`internal/guest` implements its host control operations, and `internal/run`
orchestrates a one-shot execution against a copied workspace. Its result keeps
command exit separate from host operation errors and includes changed file
contents without applying them to the source. `cmd/wolfcow-guest` builds the
static Linux guest agent. `internal/cli` owns the command surface. `task stage:a`
uses a hidden development command and requires
a macOS terminal that permits local Unix sockets and the Hypervisor framework.
`task stage:b` copies `examples/stage-b` into disposable guest storage, runs a
command in `/work`, then prints the changed files and a text diff. It does not
write changes back to the source directory. The probe accepts regular files
only, with a 256 file, 256 KiB per file, and 4 MiB total limit. Both probes
remain experimental.
Native C interop lives in `native/`; reproducible build and probe scripts live
in `hack/`. The backend interface and guest protocol are experimental; egress
and session packages will be added as those capabilities are implemented.

## GitHub Actions

Pull requests and pushes run Go tests, vet, the static guest cross-build, and
`task build` on GitHub's Apple Silicon `macos-15` runner. That runner can build
the bundle but cannot boot this libkrun VM because GitHub-hosted macOS runners
do not expose nested virtualization.

The separate **macOS VM conformance** workflow runs `task verify` and
`task stage:b` on a physical Apple Silicon Mac. To enable it:

1. Register a dedicated macOS ARM64 runner in the `wolfcow-vm` organization
   runner group. Restrict the group to this repository and to
   `ctx-lab/wolfcow/.github/workflows/vm.yml@refs/heads/main`.
2. Install Task, Rust/Cargo, and Apple command line tools on that Mac. The
   workflow installs Go through `actions/setup-go`.
3. Set the repository Actions variable `WOLFCOW_VM_RUNNER` to `enabled`.
4. Dispatch **macOS VM conformance** from `main` in GitHub Actions.

The VM workflow does not run pull-request code on the physical runner. Keep
this runner separate from personal files and credentials; GitHub warns that
self-hosted runners attached to public repositories can be reached by unsafe
workflow configuration. See [runner groups](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/manage-access)
and [GitHub's runner security guidance](https://docs.github.com/en/actions/reference/security/secure-use).

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
