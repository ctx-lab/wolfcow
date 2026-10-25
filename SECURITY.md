# Security status

ctx v0.0.1 is a VM boot preview, not a hardened sandbox for hostile code.
The macOS VMM runs with the invoking user's host permissions. The guest has a
private disposable rootfs and no configured network device, but host-side VMM
confinement has not been established or tested.

Only a copied rootfs is attached to the VM. A custom `--rootfs` source is copied
into temporary storage before boot; guest writes do not change the source.
Do not use a project directory or home directory as a rootfs source.

The next security gate is a macOS host confinement mechanism with negative tests
for access outside the runtime and guest storage. Keep this status visible until
those tests pass. Report suspected vulnerabilities privately to the maintainers
once a contact channel is published.
