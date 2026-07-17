# Prebuilt Firmware OTA Example

Spark Server Go expects firmware to be built outside the server, for example with Particle Workbench, Particle CLI, or a Docker/toolchain workflow. Upload the resulting `.bin` and let the server deliver it over OTA.

## 1. Start the server

```sh
make build
./bin/spark-server
```

## 2. Log in

```sh
TOKEN=$(curl -s http://localhost:8080/oauth/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=password&username=__admin__&password=adminPassword' \
  | jq -r .access_token)
```

## 3. Upload a prebuilt binary

This stores the binary under the product. It does not flash any device until you start an OTA job in the next step.

```sh
curl -s -X POST 'http://localhost:8080/v2/products/demo-product/firmwares?filename=firmware.bin&version=1&current=true' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @./firmware.bin
```

## 4. Target OTA for a product device

To pin a specific firmware version to one product device, use the Brewskey-compatible product-device update route. If the device is connected and flashable, the server attempts to queue/start OTA immediately; otherwise the desired version is used on the next device firmware update check.

```sh
curl -s -X PUT http://localhost:8080/v1/products/demo-product/devices/DEVICE_ID \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"desired_firmware_version":1}'
```

## 5. Manually start OTA for a connected device

For local development, you can explicitly start an OTA job. The server selects the product's current/default firmware unless you also provide `firmware_id`.

```sh
curl -s -X POST http://localhost:8080/v1/devices/DEVICE_ID/flash \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"product_id":"demo-product"}'
```

To flash a specific uploaded firmware:

```sh
curl -s -X POST http://localhost:8080/v1/devices/DEVICE_ID/flash \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"product_id":"demo-product","firmware_id":"FIRMWARE_ID"}'
```

The legacy Particle-style device update route also accepts a multipart binary:

```sh
curl -s -X PUT http://localhost:8080/v1/devices/DEVICE_ID \
  -H "Authorization: Bearer $TOKEN" \
  -F product_id=demo-product \
  -F file=@./firmware.bin
```

## 6. Watch progress

```sh
./bin/sparkctl -token "$TOKEN" events -device DEVICE_ID -prefix spark/flash
```
