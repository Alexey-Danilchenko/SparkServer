# AGENTS.md

Spark Server Go is a file-backed Go port of the Brewskey `spark-server` local Particle/Spark cloud: an HTTP/HTTPS API (default `:8080`) plus an encrypted TCP device listener (default `:5683`) in one process.

## Commands

```sh
make build          # bin/spark-server + bin/sparkctl (static, CGO_ENABLED=0)
go test ./...       # full suite; also `make test`
go test ./test/ -run TestName   # single test
make release        # cross-platform archives in dist/ via scripts/build-release.sh
make clean          # removes bin/ and dist/
```

CI (`.github/workflows/ci.yml`) runs `go test ./...` → `make build` → release-packaging smoke test. Tag pushes `v*` trigger the GitHub release workflow. `VERSION` defaults to `git describe --tags --always --dirty`; override with `VERSION=... make release`.

## Layout

- `cmd/spark-server` — server entrypoint; `cmd/sparkctl` — operator CLI.
- `internal/app` — composition root; wires all services and connects the TCP server into the device/firmware services as their live transport.
- `internal/protocol/*` — `coap`, `framing`, `handshake`, `keys`, `particle`, `session`, `tcp`, `device` (the device-protocol stack).
- `internal/jsonfile` — generic JSON file storage; each feature package owns its repositories on top.
- `test/` — end-to-end and "collider" integration tests (package `test`); `test/collider` is a virtual Particle device simulator.
- `examples/` — `settings.file.json` sample plus product/OTA/hardware-checklist guides.

## Gotchas

- `go.mod` has **zero external dependencies** (stdlib only, Go 1.27). Don't add dependencies casually.
- The server loads `settings.json` from the **current working directory** and falls back silently to defaults if it's missing. Copy `examples/settings.file.json` to `settings.json` to customize. All data directories are also CWD-relative; `data/` is gitignored and auto-created at startup.
- Default credentials are `__admin__` / `adminPassword`. `USE_SSL` does **not** change the port; HTTPS serves on the configured `PORT`.
- The empty root directories `spark-server/` and `sparkctl/` are stale leftovers — real code lives in `cmd/`. `test/data/` is also empty and unreferenced; tests are self-contained (`t.TempDir()`, port `0`, no external services).
- `ARCHITECTURE.md` is the canonical architecture doc (referenced by README). The other untracked drafts (`ARCHITECTURE1.md`, `ARCHITECTURE_codex.md`, `ARCHITECTURE_qwen.md`, `architecture2.md`) are AI-generated and not maintained — don't trust them over code.
- `spark-server-plan.md` is gitignored local planning, not part of the repo.

## Intentional non-goals (do not "fix")

- No server-side firmware source compilation (`/v1/binaries` and similar routes are intentionally unsupported). Workflow: build `.bin` externally (Particle Workbench/CLI), upload via product firmware routes, deliver via OTA.
- Product team and OAuth-client routes intentionally return `501 not_supported`.
- v2 API endpoints were deliberately removed (not used by the real Particle server).
- NeDB emulation and MongoDB are out of scope; file-backed JSON storage is the only backend.
- Preserve v1 route compatibility with the original Brewskey server; see `COMPATIBILITY.md` for the route/feature status matrix.
