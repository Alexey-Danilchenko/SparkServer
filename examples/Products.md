# Products API

This document is adapted for Spark Server Go from the original Brewskey `spark-server` product API notes:

- Original: https://github.com/Brewskey/spark-server/blob/dev/examples/Products.md
- Upstream repository: https://github.com/Brewskey/spark-server

The product API is not part of the simplest Spark/Particle local-cloud flow. It exists to group devices into fleets, associate prebuilt firmware with those fleets, and let connected devices receive product firmware updates. The original Brewskey implementation described this as a small, local-cloud subset of Particle's product APIs rather than a complete Particle Console replacement. Spark Server Go keeps that same practical goal.

## Scope in Spark Server Go

Spark Server Go supports the file-backed product functionality needed for local fleets:

- Create, list, get, update, and delete products.
- Associate existing devices with products.
- Store product-device metadata such as notes, denied, development, quarantined, and desired firmware version.
- Upload prebuilt product firmware binaries.
- Mark firmware as released, default, or current.
- Check whether a product device has newer firmware available.
- Start OTA delivery of a prebuilt binary to a connected device.
- Stream product-filtered events.

The following original Brewskey product-doc features are intentionally not implemented yet:

- Organization management and organization permissions.
- Product customers.
- CSV bulk device import with `import_method=many`.
- Product team management and product OAuth client management; these routes return `501 not_supported`.
- Server-side firmware source compilation. Build firmware externally and upload prebuilt `.bin` files.

MongoDB-backed storage is a future extension. This document describes the current file-backed behavior.

## Authentication

Use an admin or otherwise valid bearer token for product and firmware routes:

```sh
TOKEN=$(curl -s http://localhost:8080/oauth/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=password&username=__admin__&password=adminPassword' \
  | jq -r .access_token)
```

All examples below assume:

```sh
BASE=http://localhost:8080
PRODUCT=brew-controller
DEVICE_ID=2c0040000547343337373737
```

## Typical Setup Flow

1. Build firmware locally with Particle Workbench, Particle CLI, or another external toolchain.
2. Create a product.
3. Add claimed devices to the product.
4. Upload a prebuilt `.bin` to the product firmware API.
5. Mark that firmware current/default, or upload with `current=true`.
6. Let connected product devices discover and receive updates, or manually start a flash job.

If a device is associated with a product but is not owned by the same account, keep the original Brewskey caution in mind: use product-device metadata such as `quarantined` to control rollout safety.

## Product Routes

### List Products

```sh
curl -s "$BASE/v1/products" \
  -H "Authorization: Bearer $TOKEN"
```

### Create Product

Spark Server Go accepts a simple JSON body. `slug` or `name` is required; `id` is optional.

```sh
curl -s -X POST "$BASE/v1/products" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "product-1",
    "slug": "brew-controller",
    "name": "Brew Controller",
    "description": "Local brewing controller fleet"
  }'
```

Supported product fields:

- `id` or `product_id`
- `slug`
- `name`
- `description`

Original Brewskey examples include fields such as `hardware_version`, `platform_id`, `type`, `organization`, and `config_id`. Spark Server Go does not currently persist those fields.

### Get Product

Products can be read by ID or slug:

```sh
curl -s "$BASE/v1/products/$PRODUCT" \
  -H "Authorization: Bearer $TOKEN"
```

### Update Product

```sh
curl -s -X PUT "$BASE/v1/products/$PRODUCT" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Brew Controller Pro",
    "slug": "brew-controller-pro",
    "description": "Updated local brewing controller fleet"
  }'
```

### Delete Product

```sh
curl -s -X DELETE "$BASE/v1/products/$PRODUCT" \
  -H "Authorization: Bearer $TOKEN"
```

### Get Product Config

```sh
curl -s "$BASE/v1/products/$PRODUCT/config" \
  -H "Authorization: Bearer $TOKEN"
```

Response shape:

```json
{
  "product_configuration": {
    "id": "product-1",
    "product_id": "product-1",
    "slug": "brew-controller",
    "name": "Brew Controller",
    "owner_id": "__admin__"
  }
}
```

## Product Devices

### List Product Devices

```sh
curl -s "$BASE/v1/products/$PRODUCT/devices" \
  -H "Authorization: Bearer $TOKEN"
```

### Add One Device

The original Brewskey document describes both single-device and CSV import modes. Spark Server Go currently supports single-device association only.

```sh
curl -s -X POST "$BASE/v1/products/$PRODUCT/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"device_id\":\"$DEVICE_ID\"}"
```

Accepted device ID fields:

- `device_id`
- `deviceID`
- `id`
- `coreid`

### Get Product Device

```sh
curl -s "$BASE/v1/products/$PRODUCT/devices/$DEVICE_ID" \
  -H "Authorization: Bearer $TOKEN"
```

### Update Product Device

```sh
curl -s -X PUT "$BASE/v1/products/$PRODUCT/devices/$DEVICE_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "notes": "bench test unit",
    "quarantined": false,
    "development": true,
    "desired_firmware_version": 2
  }'
```

Supported product-device fields:

- `notes`
- `denied`
- `development`
- `quarantined`
- `desired_firmware_version`

`desired_firmware_version` is the Brewskey-compatible device-specific targeting path: set it on `/v1/products/$PRODUCT/devices/$DEVICE_ID`, and the server will select that firmware version for that device instead of the product default/current firmware. If the device is connected and flashable, the update route also attempts to queue/start OTA immediately; otherwise the locked version is used on the next product firmware update check.

### Remove Product Device

```sh
curl -s -X DELETE "$BASE/v1/products/$PRODUCT/devices/$DEVICE_ID" \
  -H "Authorization: Bearer $TOKEN"
```

## Product Firmware

Spark Server Go only handles prebuilt binaries. It does not compile source code.

### List Firmware

```sh
curl -s "$BASE/v1/products/$PRODUCT/firmware" \
  -H "Authorization: Bearer $TOKEN"
```

### Upload Multipart Firmware

```sh
curl -s -X POST "$BASE/v1/products/$PRODUCT/firmware" \
  -H "Authorization: Bearer $TOKEN" \
  -F version=2 \
  -F current=true \
  -F title="Brew Controller v2" \
  -F description="Workbench-built firmware" \
  -F binary=@./firmware.bin
```

Accepted multipart file field names:

- `file`
- `binary`
- `firmware`
- `firmware.bin`

### Upload Raw Binary Firmware

```sh
curl -s -X POST "$BASE/v1/products/$PRODUCT/firmware?filename=firmware.bin&version=2&current=true" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @./firmware.bin
```

### Get Firmware

Firmware can be read by ID or version:

```sh
curl -s "$BASE/v1/products/$PRODUCT/firmware/2" \
  -H "Authorization: Bearer $TOKEN"
```

### Update Firmware Metadata

This matches the original Brewskey development workflow where a firmware record can be updated after upload.

```sh
curl -s -X PUT "$BASE/v1/products/$PRODUCT/firmware/FIRMWARE_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Brew Controller v2.1",
    "description": "Updated metadata",
    "released": true,
    "current": true
  }'
```

Supported metadata fields:

- `version`
- `title`
- `description`
- `filename`
- `content_type`
- `release_notes` or `releaseNotes`
- `released` or `release`
- `default`
- `current`

### Replace Firmware Binary

Use `PUT` with a raw binary body when you rebuild locally and want to replace the binary behind an existing firmware record:

```sh
curl -s -X PUT "$BASE/v1/products/$PRODUCT/firmware/FIRMWARE_ID?filename=firmware.bin&current=true" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @./firmware.bin
```

Multipart replacement is also accepted with the same file field names as upload.

### Delete Firmware

```sh
curl -s -X DELETE "$BASE/v1/products/$PRODUCT/firmware/FIRMWARE_ID" \
  -H "Authorization: Bearer $TOKEN"
```

Delete removes both the metadata record and the stored binary file. It returns a conflict if a queued or running flash job references that firmware.

### Release Firmware

```sh
curl -s -X POST "$BASE/v1/products/$PRODUCT/firmware/FIRMWARE_ID/release" \
  -H "Authorization: Bearer $TOKEN"
```

### Set Default Firmware

Setting default also makes the firmware current/released.

```sh
curl -s -X PUT "$BASE/v1/products/$PRODUCT/firmware/FIRMWARE_ID/default" \
  -H "Authorization: Bearer $TOKEN"
```

### Check for Firmware Updates

```sh
curl -s "$BASE/v1/products/$PRODUCT/firmware/check?device_id=$DEVICE_ID&version=1" \
  -H "Authorization: Bearer $TOKEN"
```

Response shape:

```json
{
  "update_available": true,
  "firmware_id": "uploaded-firmware-id",
  "version": 2,
  "url": "/v1/products/product-1/firmware/uploaded-firmware-id"
}
```

## Manual OTA Flash Jobs

Product firmware auto-update checks happen when a connected device reports its description/hello metadata. You can also manually start OTA for a connected device.

### Start Device Flash

```sh
curl -s -X POST "$BASE/v1/devices/$DEVICE_ID/flash" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"product_id\":\"$PRODUCT\"}"
```

To flash a specific firmware record:

```sh
curl -s -X POST "$BASE/v1/devices/$DEVICE_ID/flash" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"product_id\":\"$PRODUCT\",\"firmware_id\":\"FIRMWARE_ID\"}"
```

### List Device Flash Jobs

```sh
curl -s "$BASE/v1/devices/$DEVICE_ID/flash" \
  -H "Authorization: Bearer $TOKEN"
```

### Get Device Flash Job

```sh
curl -s "$BASE/v1/devices/$DEVICE_ID/flash/JOB_ID" \
  -H "Authorization: Bearer $TOKEN"
```

Flash jobs emit `spark/flash/*` events that can be watched with:

```sh
./bin/sparkctl -token "$TOKEN" events -device "$DEVICE_ID" -prefix spark/flash
```

## Product Events

```sh
curl -N "$BASE/v1/products/$PRODUCT/events" \
  -H "Authorization: Bearer $TOKEN"
```

Prefix-filtered stream:

```sh
curl -N "$BASE/v1/products/$PRODUCT/events/spark/flash" \
  -H "Authorization: Bearer $TOKEN"
```

## Unsupported Original Product Routes

The original Brewskey document includes some routes or behaviors that Spark Server Go does not currently implement:

| Original behavior | Spark Server Go behavior |
| --- | --- |
| CSV product-device import with `import_method=many` | Not implemented; add devices one at a time. |
| Organization/customer/team management | Not implemented. |
| Product OAuth client management | Route returns `501 not_supported`. |
| Server-side firmware source compilation | Explicitly not supported. |
