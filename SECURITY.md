# Security status

Wolfcow v0.0.1 is a VM boot preview, not a hardened sandbox for hostile code.
The macOS VMM runs with the invoking user's host permissions. The guest has a
private disposable rootfs and no configured network device by default, but
host-side VMM confinement has not been established or tested.

The patched gvproxy disables direct guest TCP and UDP forwarding; unknown
guest DNS names receive NXDOMAIN. Normal runs attach no network device. An
internal per-run policy can explicitly allow a DNS hostname and TCP port via
HTTP CONNECT. The host resolves that name once, rejects private, local, and
selected special-purpose IPv4 results (including mixed answers), then dials a
selected literal IP. The guest cannot reach the
proxy's mutable forwarding API. A local synthetic endpoint remains for the
hidden `spike net` probe.

An allowed hostname is bound only during host resolution and dial. The guest
controls TLS SNI and HTTP `Host` inside the opaque tunnel, so an allowed name
on a shared CDN address may expose other tenants on that address. This is a
known limitation, not application-level hostname enforcement.

CONNECT carries opaque TCP bytes. Wolfcow does not inspect the tunneled
protocol or validate its TLS certificate; the guest client must do that. No
credential substitution is implemented. A physical macOS VM probe established
an explicitly allowed CONNECT tunnel to `example.com:80`, sent an HTTP request,
and received a response. It also denied unlisted CONNECT hostnames and ports,
raw-IP CONNECT, and direct raw-IP traffic. TLS validation and the full negative
egress suite remain open. CONNECT tunnels have a two-minute idle deadline. The
proxy forwards half-closes where the transport supports them and closes fully
otherwise; this behavior has local tests but no dedicated physical-VM check.
It also does not establish host-process confinement.
Do not run untrusted guest code with networking enabled.

Only a copied rootfs is attached to the VM. A custom `--rootfs` source is copied
into temporary storage before boot; guest writes do not change the source.
Do not use a project directory or home directory as a rootfs source.

The next security gate is a macOS host confinement mechanism with negative tests
for access outside the runtime and guest storage. Keep this status visible until
those tests pass. Report suspected vulnerabilities privately to the maintainers
once a contact channel is published.
