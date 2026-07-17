# Release Process

Spark Server Go ships as compiled executables, not as `go run` commands. Release artifacts are generated from the repository root and attached to GitHub releases.

## Versioning

Use semantic-style tags:

- `v0.x.y` while compatibility is still being proven against local hardware.
- `v1.0.0` once the file-backed server has passed collider tests, release packaging, and real-device smoke testing.
- Patch releases for bug fixes that do not change route behavior or storage formats.
- Minor releases for compatible feature additions, new tooling, or new release targets.
- Major releases only for intentional compatibility or storage-breaking changes.

## Local Release Build

Run the full test suite and build all default artifacts:

```sh
go test ./...
make release
```

The default release set includes:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `linux/arm/v6` for Raspberry Pi/ARMv6
- `windows/amd64`
- `windows/arm64`

Artifacts are written to `dist/` as `.tar.gz` archives, with SHA-256 checksums in `dist/checksums.txt`. Each archive contains both executables plus `README.md`, `COMPATIBILITY.md`, `RELEASE.md`, and `examples/`.

## Custom Release Build

Build a subset of targets:

```sh
VERSION=v0.1.0 PLATFORMS="linux/amd64 linux/arm64 linux/arm/6" ./scripts/build-release.sh
```

Platform format is:

- `GOOS/GOARCH`
- `GOOS/GOARCH/GOARM` for ARM variants such as `linux/arm/6`

## GitHub Release

1. Ensure the working tree is clean.
2. Run `go test ./...`.
3. Tag the release:

   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```

4. GitHub Actions builds the release archives, uploads them as workflow artifacts, and attaches them to the GitHub release for version tags.

## Release Candidate Checklist

- `go test ./...` passes.
- `make build` produces `bin/spark-server` and `bin/sparkctl`.
- `make release` produces all archives and `checksums.txt`.
- `COMPATIBILITY.md` reflects the intended route and feature surface.
- `README.md` documents compiled executable usage.
- Collider tests pass for provisioning, live variables/functions, webhooks, chaos, and OTA reconstruction.
- Real-device smoke testing is completed before declaring a stable `v1.0.0`.
