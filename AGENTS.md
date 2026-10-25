# Working on Wolfcow

Wolfcow is a public Apple Silicon macOS project. Keep plans in the separate
planning project; this repository holds implementation and public docs.

- Use **Wolfcow** in prose and `wolfcow` for the command, Go module, archive,
  and runtime filenames.
- Keep the CLI in `internal/cli`, VM and rootfs handling in `internal/vmm`,
  guest protocol in `internal/protocol`, native helper code in `native/`, and
  build and probe scripts in `hack/`.
- Preserve the v0.0.1 security statement in `SECURITY.md`: guest isolation
  exists, but host VMM confinement is unproven. Do not call it a hardened
  sandbox until host-side negative tests pass.
- Treat any supplied rootfs as a source that is copied before boot. Guest
  writes must never modify it. Do not suggest using a project or home
  directory as a rootfs source.
- Keep secrets out of files and Git history. CI scans the full Git history
  with Gitleaks; run a local scan before publishing if one is available.
- Run `task verify` after Go changes. Run `task build` and a shell smoke test
  on Apple Silicon macOS when changing the runtime path. Document any checks
  that cannot run locally.
