# Security status

Wolfcow v0.0.1 is a VM boot preview, not a hardened sandbox for hostile code.
The macOS VMM runs with the invoking user's host permissions. The guest has a
private disposable rootfs and no configured network device by default, but
host-side VMM confinement has not been established or tested.

Normal runs attach no network device. The patched gvproxy disables direct
guest TCP and UDP forwarding, and unknown guest DNS names receive NXDOMAIN. An
internal per-run policy can allow exact DNS hostname and TCP port pairs through
HTTP CONNECT. The host resolves an allowed name once, rejects private, local,
and selected special-purpose IPv4 results (including mixed answers), then dials
a selected literal IP. The guest cannot reach the proxy's mutable forwarding
API. A local synthetic endpoint serves the hidden `spike net` probe.

CONNECT carries opaque TCP bytes. Wolfcow does not inspect the tunneled
protocol or validate its TLS certificate; the guest client must do that. An
allowed hostname is bound only during host resolution and dial: the guest
controls TLS SNI and HTTP `Host` inside the tunnel, so an allowed name on a
shared CDN address may expose other tenants on that address. Tunnels close
after two minutes without traffic. No credential substitution is implemented.

Verified on physical Apple Silicon macOS: HTTP data through an allowed tunnel
and prompt denial of unlisted hostnames and ports, raw-IP CONNECT, and direct
raw-IP traffic. Not verified: TLS, IPv6, half-close behavior inside a VM, and
a complete negative egress suite. Networking does not establish host-process
confinement. Do not run untrusted guest code with networking enabled.

Only a copied rootfs is attached to the VM. A custom `--rootfs` source is copied
into temporary storage before boot; guest writes do not change the source.
Do not use a project directory or home directory as a rootfs source.

Host-side confinement of the macOS VMM is not implemented. This status stays
until negative tests show the VMM cannot access files outside the runtime and
guest storage. Report suspected vulnerabilities privately to the maintainers
once a contact channel is published.
