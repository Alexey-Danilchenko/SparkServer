# Local Hardware Smoke Test Checklist

Use this only after the file-backed server, documentation, and collider tests are passing. The collider tests are the main compatibility suite; real hardware is the final smoke test.

## Before repointing devices

- Back up current Particle/device configuration and know how to restore the Particle cloud key.
- Start Spark Server Go on a stable local IP address.
- Confirm `settings.json` uses file storage and persistent `data/` directories.
- Confirm `sparkctl` can log in and list claimed devices.
- Keep local/USB access available for every test device in case it needs recovery.

## Server checks

```sh
go test ./...
make build
./bin/spark-server
```

In another terminal:

```sh
TOKEN=$(./bin/sparkctl -username __admin__ -password adminPassword login)
./bin/sparkctl -token "$TOKEN" devices
./bin/sparkctl -token "$TOKEN" events
```

## Device checks

1. Register/send the device public key to the local server.
2. Claim the device under the test account.
3. Repoint the device server key/host to the local Spark Server TCP endpoint.
4. Confirm it appears online with `sparkctl devices`.
5. Read a known variable with `sparkctl variable DEVICE_ID VARIABLE_NAME`.
6. Call a safe test function with `sparkctl function DEVICE_ID FUNCTION_NAME ARGUMENT`.
7. Publish a device event and verify it appears in `sparkctl events`.
8. Upload a prebuilt test `.bin` and start OTA.
9. Watch `spark/flash/*` events until completion.
10. Confirm the device reconnects and still responds to variable/function calls.

## Recovery notes

If a device stops responding, restore Particle cloud configuration using the normal Particle CLI/key recovery flow while the device is in a local recovery mode such as DFU/listening mode.
