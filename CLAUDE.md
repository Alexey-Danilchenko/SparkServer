# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Spark Server Go is a file-backed Go port of the Brewskey `spark-server` local Particle/Spark cloud: an HTTP/HTTPS API (default `:8080`) plus an encrypted TCP device listener (default `:5683`) in one process. `ARCHITECTURE.md` is the canonical architecture doc (package map, sequence diagrams, runtime flows) — read it before changing behavior. `COMPATIBILITY.md` is the route/feature status matrix against the original Brewskey server.

## Commands

```sh
make build          # bin/spark-server + bin/sparkctl (static, CGO_ENABLED=0)
go test ./...       # full suite; also `make test`
go test ./test/ -run TestName   # single test
make release        # cross-platform archives in dist/ via scripts/build-release.sh
make clean          # removes bin/ and dist/
```

CI (`.github/workflows/ci.yml`) runs `go test ./...` → `make build` → release-packaging smoke test. Tag pushes `v*` trigger the GitHub release workflow. `VERSION` defaults to `git describe --tags --always --dirty`; override with `VERSION=... make release`.

## Architecture (big picture)

One process, two network surfaces, one set of in-process domain services. Both the HTTP API and the TCP device protocol drive the same service instances, so state changes made from either surface are immediately visible to the other.

- `internal/app` is the composition root. Key wiring: `events.Service` has `webhooks.Service` registered as a sink; `devices.Service` uses `tcp.Server` as its `LiveDeviceClient`; `firmware.Service` uses `tcp.Server` as its `FlashTransport`; `tcp.Server` sends device-originated messages to `protocol/device.Handler`, which persists descriptions, publishes events, and schedules automatic firmware checks *after* the description ACK is written.
- The device protocol is deliberately layered, each layer testable in isolation: `framing` (2-byte big-endian length prefix, bounded 64 KiB) → `handshake` (RSA PKCS1 v1.5 decrypts the session key from the first frame) → `session` (AES-128-CBC + HMAC-SHA1; HMAC verified *before* decryption; any failure terminates the session) → `coap` (Particle-flavored packet codec) → `protocol/device` dispatch. `protocol/particle` holds the path aliases (`v`, `f`, `d`, `e`, `u`, `c`).
- `internal/jsonfile` keeps its generic one-file-per-record store private and exposes repositories satisfying interfaces owned by the consuming domain packages, so services stay storage-agnostic. Records are `<validated-id>.json`; writes are temp-file + rename; each repository has an in-process mutex; lists are filename-sorted for determinism.
- **Dependency direction rule (follow when adding features):** define the smallest storage/transport interface in the domain package that consumes it, implement adapters in `jsonfile`, `httpapi`, or `protocol/tcp`, and connect concrete implementations only in `internal/app`.
- Tests: focused unit tests live beside their packages; `test/` holds cross-package end-to-end tests, including "collider" tests where `test/collider` (a virtual Particle device simulator) drives the real TCP and HTTP paths together. Tests are self-contained: `t.TempDir()`, port `0`, no external services.

## Gotchas

- `go.mod` has **zero external dependencies** (stdlib only, Go 1.27). Don't add dependencies casually.
- The server loads `settings.json` from the **current working directory** and falls back silently to defaults if it's missing. Copy `examples/settings.file.json` to `settings.json` to customize. All data directories are CWD-relative; `data/` is gitignored and auto-created at startup.
- Default credentials are `__admin__` / `adminPassword`. `USE_SSL` does **not** change the port; HTTPS serves on the configured `PORT`.
- The HTTP server intentionally has **no** `WriteTimeout` because SSE connections are long-lived. Webhook retries are event-driven (stored `NextAttemptAt` gates delivery; backoff 1 min → 1 h), with no background retry worker.
- The server trusts a successfully decrypted handshake's device ID; the handshake does not verify a device signature with the stored public key.
- The empty root directories `spark-server/` and `sparkctl/` are stale leftovers — real code lives in `cmd/`. `test/data/` is also empty and unreferenced.
- `ARCHITECTURE1.md`, `ARCHITECTURE_codex.md`, `ARCHITECTURE_qwen.md`, `architecture2.md` are untracked AI-generated drafts — don't trust them over code. `spark-server-plan.md` is gitignored local planning.

## Intentional non-goals (do not "fix")

- No server-side firmware source compilation (`/v1/binaries` and similar routes are intentionally unsupported). Workflow: build `.bin` externally (Particle Workbench/CLI), upload via product firmware routes, deliver via OTA.
- Product team and OAuth-client routes intentionally return `501 not_supported`.
- v2 API endpoints were deliberately removed (not used by the real Particle server).
- NeDB emulation and MongoDB are out of scope; file-backed JSON storage is the only backend (`DB_CONFIG` is retained for compatibility only).
- Preserve v1 route compatibility with the original Brewskey server; consult `COMPATIBILITY.md` before adding route aliases.
