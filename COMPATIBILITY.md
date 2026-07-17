# Spark Server Go Compatibility Matrix

This matrix tracks the file-backed Go port against the Brewskey `spark-server` behavior that matters for Particle/Spark local-cloud compatibility.

Status values:

- **Implemented** — available in the Go port and covered by unit, HTTP, protocol, or collider tests.
- **Implemented, intentionally limited** — route exists with behavior matching the useful/original local-cloud path, but does not attempt unsupported Particle cloud features.
- **Intentionally unsupported** — not part of this port.
- **Future extension** — out of the file-backed release scope.

## Runtime Compatibility

| Area | Status | Notes |
| --- | --- | --- |
| HTTP API listener | Implemented | Defaults to `0.0.0.0:8080`. |
| TCP device listener | Implemented | Defaults to `0.0.0.0:5683`. |
| File-backed persistence | Implemented | Runtime data is stored under `data/` by default. |
| NeDB persistence | Intentionally unsupported | Go port uses JSON file stores rather than emulating NeDB internals. |
| MongoDB persistence | Future extension | Planned after file-backed release completion. |
| `settings.json` style config | Implemented | Legacy-style uppercase keys are accepted. |
| Server-side firmware source compilation | Intentionally unsupported | Build with Particle Workbench/CLI or external tooling, then upload prebuilt `.bin` files. |

## Auth and Users

| Route | Status | Notes |
| --- | --- | --- |
| `POST /oauth/token` | Implemented | Password grant compatible with local Spark/Particle clients. |
| `POST /v1/users` | Implemented | Creates file-backed users. |
| `GET /v1/access_tokens` | Implemented | Lists access tokens for authenticated user. |
| `DELETE /v1/access_tokens/{token}` | Implemented | Deletes matching access token. |

## Devices and Provisioning

| Route | Status | Notes |
| --- | --- | --- |
| `POST /v1/device_claims` | Implemented | Creates claim codes. |
| `POST /v1/provisioning/{deviceID}` | Implemented | Supports claim-code provisioning and authenticated public-key registration. |
| `POST /v1/devices` | Implemented | Claims devices to the authenticated user. |
| `GET /v1/devices` | Implemented | Lists user devices. |
| `GET /v1/devices/{deviceIDOrName}` | Implemented | Returns stored and live device metadata. |
| `GET /v1/devices/{deviceIDOrName}/{varName}` | Implemented | Reads variables from connected TCP devices. |
| `POST /v1/devices/{deviceIDOrName}/{functionName}` | Implemented | Calls functions on connected TCP devices. |
| `PUT /v1/devices/{deviceIDOrName}` | Implemented | Supports rename, signal acknowledgement, `app_id` flash requests, and multipart `.bin` OTA upload. |
| `DELETE /v1/devices/{deviceIDOrName}` | Implemented | Unclaims/removes user device association. |
| `PUT /v1/devices/{deviceIDOrName}/ping` | Implemented | Sends live TCP ping when device is connected. |

## Events

| Route | Status | Notes |
| --- | --- | --- |
| `POST /v1/ping` | Implemented | Health-style ping route. |
| `POST /v2/ping` | Implemented | v2 equivalent. |
| `GET /v1/events` | Implemented | Authenticated SSE stream. |
| `GET /v1/events/{prefix}` | Implemented | Prefix-filtered SSE stream. |
| `GET /v1/devices/events` | Implemented | Device event stream. |
| `GET /v1/devices/{deviceIDOrName}/events` | Implemented | Device-filtered event stream. |
| `GET /v1/devices/{deviceIDOrName}/events/{prefix}` | Implemented | Device and prefix-filtered event stream. |
| `POST /v1/devices/events` | Implemented | Authenticated event publish route. |
| v2 event equivalents | Implemented | Mirrors v1 event behavior. |

## Products and Fleets

| Route | Status | Notes |
| --- | --- | --- |
| `GET /v1/products` | Implemented | Lists products. |
| `POST /v1/products` | Implemented | Creates products. |
| `GET /v1/products/{productIDOrSlug}` | Implemented | Gets product by ID or slug. |
| `GET /v1/products/{productIDOrSlug}/config` | Implemented | Returns product config metadata. |
| `GET /v1/products/{productIDOrSlug}/events` | Implemented | Product-filtered SSE stream. |
| `GET /v1/products/{productIDOrSlug}/events/{prefix}` | Implemented | Product and prefix-filtered SSE stream. |
| `PUT /v1/products/{productIDOrSlug}` | Implemented | Updates product metadata. |
| `DELETE /v1/products/{productIDOrSlug}` | Implemented | Deletes product metadata. |
| `GET /v1/products/{productIDOrSlug}/devices` | Implemented | Lists product devices. |
| `POST /v1/products/{productIDOrSlug}/devices` | Implemented | Adds/associates product device. |
| `GET /v1/products/{productIDOrSlug}/devices/{deviceID}` | Implemented | Gets product-device association. |
| `PUT /v1/products/{productIDOrSlug}/devices/{deviceID}` | Implemented | Updates product-device association, including Brewskey-compatible `desired_firmware_version` targeting. |
| `DELETE /v1/products/{productIDOrSlug}/devices/{deviceID}` | Implemented | Removes product-device association. |
| `GET /v2/products/count` | Implemented | Product count helper. |
| `GET /v2/products/{productIDOrSlug}/devices/count` | Implemented | Product-device count helper. |
| v2 product CRUD and device equivalents | Implemented | Mirrors v1 product behavior. |
| Product team routes | Implemented, intentionally limited | Return `501 not_supported`, matching unsupported original local-cloud behavior. |
| Product OAuth-client routes | Implemented, intentionally limited | Return `501 not_supported`, matching unsupported original local-cloud behavior. |

## Firmware and OTA

| Route | Status | Notes |
| --- | --- | --- |
| `GET /v1/products/{productIDOrSlug}/firmware` | Implemented | Lists product firmware metadata. |
| `POST /v1/products/{productIDOrSlug}/firmware` | Implemented | Uploads prebuilt firmware binaries. |
| `GET /v1/products/{productIDOrSlug}/firmware/check` | Implemented | Product firmware update check. |
| `GET /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}` | Implemented | Gets firmware by ID or version. |
| `PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}` | Implemented | Updates metadata and can replace the prebuilt binary. |
| `DELETE /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}` | Implemented | Deletes metadata and binary unless an active flash job references it. |
| `POST\|PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}/release` | Implemented | Marks firmware released. |
| `POST\|PUT /v1/products/{productIDOrSlug}/firmware/{firmwareIDOrVersion}/default` | Implemented | Marks firmware default/current. |
| v2 product firmware read routes | Implemented | Matches Brewskey plural `/firmwares` list/count/get routes. |
| v2 product firmware management aliases | Implemented, additive | Create/update/delete/release/default mirror v1 for local operator convenience. |
| `GET /v1/devices/{deviceIDOrName}/flash` | Implemented | Lists flash jobs for a device. |
| `POST /v1/devices/{deviceIDOrName}/flash` | Implemented | Starts OTA for a connected device. |
| `GET /v1/devices/{deviceIDOrName}/flash/{jobID}` | Implemented | Reads OTA job status. |
| v2 device flash equivalents | Implemented | Mirrors v1 flash job behavior. |
| `/v1/binaries` source compilation routes | Intentionally unsupported | Source compilation is not part of this port. |

## Webhooks

| Route | Status | Notes |
| --- | --- | --- |
| `GET /v1/webhooks` | Implemented | Lists webhooks. |
| `POST /v1/webhooks` | Implemented | Creates webhook definitions. |
| `GET /v1/webhooks/{webhookID}` | Implemented | Gets webhook definition. |
| `PUT /v1/webhooks/{webhookID}` | Implemented | Updates webhook definition. |
| `DELETE /v1/webhooks/{webhookID}` | Implemented | Deletes webhook definition. |
| v2 webhook equivalents | Implemented | Mirrors v1 webhook behavior. |
| Event delivery | Implemented | Supports exact, wildcard, and prefix matching plus persisted delivery status. |
| Advanced webhook policy tuning | Future extension | Existing retry/backoff behavior is implemented; exposing advanced operator knobs can be added later. |

## Protocol and Collider Coverage

| Area | Status | Notes |
| --- | --- | --- |
| TCP handshake/session encryption | Implemented | RSA session material plus AES-128-CBC/HMAC-SHA1 session codec. |
| CoAP-style message encode/decode | Implemented | Covered by protocol tests. |
| Device variables/functions | Implemented | Covered by live collider tests. |
| Device events | Implemented | Covered by HTTP/live collider tests. |
| Webhook flows | Implemented | Covered by collider webhook tests. |
| Chaos/stress flow | Implemented | Randomized provisioning, device churn, variables, functions, and webhooks. |
| OTA reconstruction | Implemented | Virtual device receives OTA chunks and reconstructs the binary locally. |
| Real hardware smoke test | Manual final check | Deferred until release candidate is ready and devices can be safely repointed. |
