# Spark Server Go

Spark Server Go is a Go port of the [Brewskey `spark-server`](https://github.com/Brewskey/spark-server) local cloud. The goal is to preserve compatibility with Particle/Spark local cloud behavior while moving the implementation from TypeScript/Node.js to a modular Go service.

This project was vibe-coded with Codex LLM.

## Current Scope

This repository contains a file-backed Go implementation of the Spark/Particle local cloud path needed for local development and final real-device smoke testing:

- Config loader compatible with `settings.json` style keys
- Structured logging with `log/slog`
- HTTP listener on port `8080`
- TCP device listener on port `5683`
- Graceful shutdown on interrupt or `SIGTERM`
- Startup creation for local data directories
- Domain models for users, tokens, devices, products, firmware, webhooks, and events
- Repository interfaces plus clean JSON file-backed implementations
- Product/fleet CRUD routes plus product-device association routes
- Webhook CRUD/update routes plus event-triggered HTTP delivery, template expansion, delivery metadata, and retry backoff
- Default admin creation, password verification, OAuth token login, bearer token middleware, and access-token list/delete routes
- File-backed device claim, list, get, rename, unclaim, ping, legacy signal acknowledgement, and binary/app flash update routes
- Device claim-code creation and provisioning routes
- In-process event broker, file-backed published event records, authenticated publish route, and SSE routes
- TCP connection registry foundation and protocol-facing device online/offline state updates
- Protocol key manager for server/device RSA PEM files and bounded binary frame read/write helpers
- TCP handshake/session flow using framed hello payloads and RSA-decrypted session material
- AES-128-CBC/HMAC-SHA1 session message codec and CoAP-style packet encode/decode
- TCP session loop that decrypts framed session traffic, parses CoAP messages, dispatches device messages, and encrypts responses
- Protocol device handler for ping acknowledgements and device-originated event publishing into the file-backed/SSE event pipeline
- Live TCP command bridge for REST-initiated variable reads, function calls, and ping requests against connected devices
- Device description/hello handling that persists advertised variables, functions, and attributes for REST discovery
- Particle-style protocol aliases for variable, function, event, ping, and describe paths plus compatible function argument/query handling
- Configurable `API_TIMEOUT` for live device variable/function/ping requests with deterministic timeout errors
- Prebuilt firmware binary upload with file-backed metadata, SHA-256 checksums, and product firmware routes
- Product firmware release/default selection and product firmware update-check routes
- Product firmware auto-update checks from device describe/hello metadata, queued after the protocol ACK is sent
- OTA flash jobs for selecting uploaded firmware for connected devices and tracking queued/running/completed/failed progress
- OTA chunk manifest generation and flash job state transitions for queued/running/completed/failed progress tracking
- Particle-style TCP OTA begin negotiation using binary `UpdateBegin` payloads on protocol path `u`
- Particle-style TCP OTA chunk delivery using path `c`, CRC query options, padded chunks, fast-OTA chunk indexes, and `UpdateDone`
- OTA `ChunkMissed` handling that ACKs device retry requests and resends requested chunks, plus device abort failure handling
- Automatic OTA chunk pumping after flash start plus `spark/flash/*` progress events for queued/running/chunk/completed/failed states
- Collider tests for provisioning, live variables/functions, webhooks, chaos, and virtual OTA binary reconstruction
- `sparkctl` monitoring CLI for local hardware visibility and interaction

MongoDB storage is deferred as a future extension after the file-backed server path is complete. Server-side source compilation is intentionally not supported; build firmware externally and upload prebuilt `.bin` files for OTA delivery.

## Layout

```text
.
├── go.mod
├── Makefile
├── COMPATIBILITY.md
├── RELEASE.md
├── README.md
├── .github/workflows
├── cmd
│   ├── spark-server
│   └── sparkctl
├── examples
├── scripts
├── internal
│   ├── app
│   ├── auth
│   ├── config
│   ├── devices
│   ├── domain
│   ├── events
│   ├── firmware
│   ├── httpapi
│   ├── monitorcli
│   ├── products
│   ├── protocol
│   ├── repository
│   └── webhooks
└── test
    └── collider
```

Go source files now live at the project root under `cmd/`, `internal/`, and related package directories. Tests live under `test/`.

Runtime data is separate from source code. The server creates and uses `data/` at runtime for local file storage, and `data/` is ignored by git.

## Building

Build local executables:

```sh
make build
```

This creates:

- `bin/spark-server` — local cloud HTTP/TCP server
- `bin/sparkctl` — local monitoring/operator CLI

Equivalent direct Go commands are:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/spark-server ./cmd/spark-server
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/sparkctl ./cmd/sparkctl
```

`go run ./cmd/...` is useful while developing, but final delivery should use compiled executables.

## Release Artifacts

Build cross-platform release archives:

```sh
make release
```

By default this creates `.tar.gz` archives and `checksums.txt` in `dist/` for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `linux/arm/v6` for Raspberry Pi/ARMv6
- `windows/amd64`
- `windows/arm64`

Override the version or platform list when needed:

```sh
VERSION=v0.1.0 make release
PLATFORMS="linux/amd64 linux/arm64 linux/arm/6" ./scripts/build-release.sh
```

Each archive contains `spark-server`, `sparkctl`, `README.md`, `COMPATIBILITY.md`, `RELEASE.md`, and the `examples/` directory. These archives are suitable for attaching to a GitHub release.

See `RELEASE.md` for versioning, release-candidate checks, and GitHub release workflow details.

## Running

```sh
./bin/spark-server
```

If `settings.json` is missing, the server starts with defaults:

- HTTP port: `8080`, listening on all local interfaces
- TCP device port: `5683`, listening on all local interfaces
- Admin user: `__admin__`
- Admin password: `adminPassword`
- File storage under `data/`
- Server RSA keys under `data/default_key.pem` and `data/default_key.pub.pem`

## Monitoring CLI

`sparkctl` is a small local operator tool for safely inspecting and interacting with a running Spark Server before moving real devices over to it.

```sh
./bin/sparkctl -username __admin__ -password adminPassword devices
./bin/sparkctl -token "$SPARK_TOKEN" device <device-id-or-name>
./bin/sparkctl -token "$SPARK_TOKEN" variable <device-id-or-name> <variable>
./bin/sparkctl -token "$SPARK_TOKEN" function <device-id-or-name> <function> "argument"
./bin/sparkctl -token "$SPARK_TOKEN" events -device <device-id-or-name>
```

Global flags:

- `-base` sets the server URL, defaulting to `http://localhost:8080` or `SPARK_SERVER_URL`.
- `-token` sets an existing bearer token, or use `SPARK_TOKEN`.
- `-username` and `-password` perform automatic login, or use `SPARK_USERNAME` and `SPARK_PASSWORD`.
- `-json` prints machine-readable JSON for scripts.

## Getting started with Particle devices

1. Run the server with:

```sh
./bin/spark-server
```

2. Watch for your local IP address. Each listener and the combined server startup are logged:

```
time=2026-08-10T22:16:07.206+01:00 level=INFO msg="http listener started" server=http address=192.168.0.5:8080
time=2026-08-10T22:16:07.206+01:00 level=INFO msg="tcp listener started" server=tcp address=192.168.0.5:5683
time=2026-08-10T22:16:07.206+01:00 level=INFO msg="spark server started" http=192.168.0.5:8080 tcp=192.168.0.5:5683
```

3. We will now create a new server profile on Particle-CLI using the command:

```
particle config profile_name apiUrl  "http://DOMAIN_OR_IP"
```

For the local cloud, the port number 8080 needs to be added behind: `http://domain_or_ip:8080`. It is important to also have the `http://` otherwise it won't work.

This will create a new profile to point to your server and switching back to the spark cloud is simply `particle config particle` and other profiles would be `particle config profile_name`

4. We will now point over to the local cloud using

```
particle config profile_name
```

5. On a separate CMD from the one running the server, type

```
particle login --username __admin__ --password adminPassword
```

The default username is `__admin__` and password is `adminPassword`.

This will create an account on the local cloud
_This creates a config file located in the `%userprofile%/.particle%` folder_

Perform CTRL + C once you logon with Particle-CLI asking you to send Wifi-credentials etc...

6. Put your core into listening mode, and run

```
particle identify
```

to get your core id. You'll need this id later

7. The next steps will generate a bunch of keys for your device. I recommend `mkdir ..\temp` and `cd ..\temp`

8. Put your device in DFU mode.

9. Change server keys to local cloud key + IP Address

```
particle keys server ..\spark-server\data\default_key.pub.pem --host IP_ADDRESS --protocol tcp
```

**Note You can go back to using the particle cloud by [downloading the public key here](https://s3.amazonaws.com/spark-website/cloud_public.der).**
You'll need to run `particle config particle`, `particle keys server cloud_public.der`, and `particle keys doctor your_core_id` while your device is in DFU mode.

10. Create and provision access on your local cloud with the keys doctor:

```
   particle keys doctor your_core_id
```

**Note For Electrons and probably all newer hardware you need to run these commands**
There is either a bug in the CLI or Particle always expects these newer devices to use UDP.

Put your device in DFU mode and then:

```
particle keys new test_key --protocol tcp
particle keys load test_key.der
particle keys send XXXXXXXXXXXXXXXXXXXXXXXX test_key.pub.pem
```

---

At this point you should be able to run normal cloud commands and flash binaries. You can add any webhooks you need, call functions, or get variable values.

## Auth Routes

Implemented auth endpoints:

- `POST /oauth/token`
- `POST /v1/users`
- `GET /v1/access_tokens`
- `DELETE /v1/access_tokens/{token}`

## Device Routes

Implemented device endpoints:

- `POST /v1/device_claims`
- `POST /v1/provisioning/{deviceID}`
- `POST /v1/devices`
- `GET /v1/devices`
- `GET /v1/devices/{deviceIDOrName}`
- `GET /v1/devices/{deviceIDOrName}/{varName}`
- `POST /v1/devices/{deviceIDOrName}/{functionName}`
- `PUT /v1/devices/{deviceIDOrName}`
- `DELETE /v1/devices/{deviceIDOrName}`
- `PUT /v1/devices/{deviceIDOrName}/ping`

`POST /v1/provisioning/{deviceID}` supports both unauthenticated claim-code provisioning and authenticated Particle `sendPublicKey` style registration using `publicKey`/`public_key`/`key`, which stores the device public key and claims the device to the authenticated account.

`PUT /v1/devices/{deviceIDOrName}` supports rename requests, legacy `signal` acknowledgement, `app_id` flash requests, and multipart `.bin` uploads for custom OTA.

## Event Routes

Implemented event endpoints:

- `POST /v1/ping`
- `GET /v1/events`
- `GET /v1/events/{prefix}`
- `GET /v1/devices/events`
- `GET /v1/devices/{deviceIDOrName}/events`
- `GET /v1/devices/{deviceIDOrName}/events/{prefix}`
- `POST /v1/devices/events`

## Firmware Routes

Implemented prebuilt binary firmware metadata/storage endpoints:

- `GET /v1/products/{productIDOrSlug}/firmware`
- `POST /v1/products/{productIDOrSlug}/firmware`
- `GET /v1/products/{productIDOrSlug}/firmware/check`
- `GET /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}`
- `PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}`
- `DELETE /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}`
- `POST|PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}/release`
- `POST|PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}/default`

Implemented OTA job endpoints:

- `GET /v1/devices/{deviceIDOrName}/flash`
- `POST /v1/devices/{deviceIDOrName}/flash`
- `GET /v1/devices/{deviceIDOrName}/flash/{jobID}`

### Firmware Build Workflow

Spark Server Go intentionally does not compile Particle firmware source code. The expected workflow is the same one most local Particle developers use:

1. Build firmware locally with Particle Workbench, Particle CLI, or an external Docker/toolchain setup.
2. Upload the resulting prebuilt `.bin` through the product firmware endpoints.
3. Let Spark Server Go deliver that binary to devices via OTA.

The original Brewskey `spark-server` source-compilation endpoint was only a wrapper around an external `../spark-firmware` checkout and `make`; it did not contain or manage the Particle ARM GCC/ParticleOS toolchain itself. This Go port therefore treats server-side compilation as intentionally out of scope, not deferred work, and focuses on reliable metadata, storage, selection, and OTA delivery of prebuilt binaries.

## Product Routes

Implemented product/fleet endpoints:

- `GET /v1/products`
- `POST /v1/products`
- `GET /v1/products/{productIDOrSlug}`
- `GET /v1/products/{productIDOrSlug}/config`
- `GET /v1/products/{productIDOrSlug}/events`
- `GET /v1/products/{productIDOrSlug}/events/{prefix}`
- `PUT /v1/products/{productIDOrSlug}`
- `DELETE /v1/products/{productIDOrSlug}`
- `GET /v1/products/{productIDOrSlug}/devices`
- `POST /v1/products/{productIDOrSlug}/devices`
- `GET /v1/products/{productIDOrSlug}/devices/{deviceID}`
- `PUT /v1/products/{productIDOrSlug}/devices/{deviceID}`
- `DELETE /v1/products/{productIDOrSlug}/devices/{deviceID}`
- `POST /v1/products/{productIDOrSlug}/clients`
- `PUT /v1/products/{productIDOrSlug}/clients/{clientID}`
- `DELETE /v1/products/{productIDOrSlug}/clients/{clientID}`
- `DELETE /v1/products/{productIDOrSlug}/team/{username}`

Product team and OAuth-client routes return `501 not_supported`, matching the original server’s unsupported status for those features.
`PUT /v1/products/{productIDOrSlug}/devices/{deviceID}` supports Brewskey-style `desired_firmware_version` locks for targeting a specific firmware version to one product device.

## Webhook Routes

Implemented webhook endpoints:

- `GET /v1/webhooks`
- `POST /v1/webhooks`
- `GET /v1/webhooks/{webhookID}`
- `PUT /v1/webhooks/{webhookID}`
- `DELETE /v1/webhooks/{webhookID}`

Published events trigger matching webhooks. Webhook `event` values can be exact names, `*`, or prefix matches ending with `*`. Delivery status, last error, failure count, and next retry time are persisted with each webhook.

## Configuration

The loader accepts the legacy-style config keys used by Brewskey SparkServer:

```json
{
  "DEFAULT_ADMIN_USERNAME": "__admin__",
  "DEFAULT_ADMIN_PASSWORD": "adminPassword",
  "DEVICE_DIRECTORY": "data/devices",
  "DEVICE_CLAIMS_DIRECTORY": "data/deviceClaims",
  "EVENTS_DIRECTORY": "data/events",
  "FIRMWARE_DIRECTORY": "data/knownApps",
  "PRODUCTS_DIRECTORY": "data/products",
  "USERS_DIRECTORY": "data/users",
  "TOKENS_DIRECTORY": "data/accessTokens",
  "WEBHOOKS_DIRECTORY": "data/webhooks",
  "SERVER_KEYS_DIRECTORY": "data",
  "LOGIN_ROUTE": "/oauth/token",
  "ACCESS_TOKEN_LIFETIME": 7776000,
  "API_TIMEOUT": 30000,
  "EXPRESS_SERVER_CONFIG": {
    "PORT": 8080,
    "USE_SSL": false,
    "SSL_CERTIFICATE_FILEPATH": null,
    "SSL_PRIVATE_KEY_FILEPATH": null
  },
  "TCP_DEVICE_SERVER_CONFIG": {
    "PORT": 5683
  },
  "DB_CONFIG": {
    "DB_TYPE": "file"
  }
}
```

To serve the API over HTTPS, set `USE_SSL` to `true` and provide PEM certificate and private-key paths:

```json
"EXPRESS_SERVER_CONFIG": {
  "PORT": 443,
  "USE_SSL": true,
  "SSL_CERTIFICATE_FILEPATH": "/etc/letsencrypt/live/example/fullchain.pem",
  "SSL_PRIVATE_KEY_FILEPATH": "/etc/letsencrypt/live/example/privkey.pem"
}
```

**NOTE:**`USE_SSL` does not change the port automatically; HTTPS uses the configured `PORT`. When enabled, startup logs report `https listener started`.

See `examples/settings.file.json` for a ready-to-copy file-backed configuration.

## Examples

- `examples/settings.file.json` — complete file-backed `settings.json` sample.
- `examples/Products.md` — adapted product/fleet API guide with reference to the original Brewskey document.
- `examples/prebuilt-firmware-ota.md` — upload a Particle Workbench/CLI-built `.bin` and start OTA.
- `examples/local-hardware-checklist.md` — final real-device smoke test checklist after collider tests pass.

## Compatibility

See `COMPATIBILITY.md` for the route and feature matrix against the original Brewskey `spark-server` behavior.

## Testing

```sh
go test ./...
```

## Porting Assumptions

Confirmed project direction:

- Build gradually, with full functional parity by the end of the port.
- Keep Spark/Particle-compatible auth and route behavior.
- Prioritize file-backed storage; keep MongoDB possible as a later configurable backend.
- Support all Particle TCP-capable devices.
- Do not implement server-side firmware compilation; build locally with Particle Workbench/CLI or an external toolchain.
- Treat source-compilation routes such as `/v1/binaries` as intentionally unsupported unless a future external build-service integration is explicitly designed.
- Support OTA flashing of prebuilt `.bin` firmware binaries.
- Preserve v1 route compatibility.
- Defer MongoDB until after the file-backed implementation is complete and stable.
