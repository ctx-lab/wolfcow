# ctx

A local microVM runtime for coding agents. The current preview boots a disposable
Linux shell on Apple Silicon macOS using libkrun.

```sh
task build
./bin/ctx shell
```

Type `exit` to leave the VM. This is an early preview for local experiments;
it is not yet intended for running untrusted workloads.

See [development](docs/development.md) for build requirements and commands,
[security](SECURITY.md) for the current boundary, and
[third-party components](THIRD_PARTY.md) for runtime sources and licenses.

Licensed under [Apache-2.0](LICENSE).
