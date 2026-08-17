package firmware

import (
	"context"
	"strconv"
	"strings"

	"sparkserver/internal/devices"
)

func (service *Service) CheckProductFirmwareUpdate(
	ctx context.Context,
	request UpdateCheckRequest,
) (*ProductFirmware, bool, error) {
	if request.ProductID == "" {
		return nil, false, ErrNotFound
	}

	target, err := service.targetProductFirmware(ctx, request.ProductID, request.TargetVersion)
	if err != nil {
		return nil, false, err
	}
	if request.TargetVersion == nil && !target.Released && !target.Current && !target.Default {
		return nil, false, nil
	}
	if request.CurrentFirmware != "" && request.CurrentFirmware == target.ID {
		return target, false, nil
	}
	if request.CurrentVersion >= target.Version && request.CurrentVersion != 0 {
		return target, false, nil
	}
	return target, true, nil
}

func (service *Service) CheckAndStartProductFirmwareUpdate(
	ctx context.Context,
	device *devices.Device,
) (*FlashJob, bool, error) {
	if device == nil || device.ID == "" {
		return nil, false, ErrNotFound
	}

	productID := productIDForDevice(device)
	if productID == "" {
		return nil, false, nil
	}

	targetVersion, err := service.desiredFirmwareVersion(ctx, productID, device.ID)
	if err != nil {
		return nil, false, err
	}
	target, updateAvailable, err := service.CheckProductFirmwareUpdate(ctx, UpdateCheckRequest{
		DeviceID:       device.ID,
		ProductID:      productID,
		TargetVersion:  targetVersion,
		CurrentVersion: firmwareVersionForDevice(device),
		CurrentFirmware: firstNonEmptyString(
			device.Attributes["firmware_id"],
			device.Attributes["firmwareID"],
			device.Attributes["product_firmware_id"],
		),
	})
	if err != nil || !updateAvailable || target == nil {
		return nil, false, err
	}
	if service.hasFlashJobForDevice(ctx, device.ID, target.ID) {
		return nil, false, nil
	}

	job, err := service.StartDeviceFlash(ctx, FlashRequest{
		DeviceID:   device.ID,
		ProductID:  productID,
		FirmwareID: target.ID,
	})
	if err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (service *Service) CurrentProductFirmware(
	ctx context.Context,
	productID string,
) (*ProductFirmware, error) {
	firmwares, err := service.ListProductFirmware(ctx, productID)
	if err != nil {
		return nil, err
	}

	for index := range firmwares {
		if firmwares[index].Current || firmwares[index].Default {
			return &firmwares[index], nil
		}
	}
	if len(firmwares) == 0 {
		return nil, ErrNotFound
	}
	return &firmwares[len(firmwares)-1], nil
}

func (service *Service) targetProductFirmware(
	ctx context.Context,
	productID string,
	targetVersion *int,
) (*ProductFirmware, error) {
	if targetVersion == nil {
		return service.CurrentProductFirmware(ctx, productID)
	}
	return service.GetProductFirmware(ctx, productID, strconv.Itoa(*targetVersion))
}

func (service *Service) desiredFirmwareVersion(
	ctx context.Context,
	productID string,
	deviceID string,
) (*int, error) {
	if service.productDevices == nil || productID == "" || deviceID == "" {
		return nil, nil
	}

	return service.productDevices.DesiredFirmwareVersion(ctx, productID, deviceID)
}

func (service *Service) selectFirmware(
	ctx context.Context,
	productID string,
	firmwareID string,
) (*ProductFirmware, error) {
	if firmwareID != "" {
		return service.GetProductFirmware(ctx, productID, firmwareID)
	}
	return service.CurrentProductFirmware(ctx, productID)
}

func productIDForDevice(device *devices.Device) string {
	if device.ProductID != "" {
		return device.ProductID
	}
	if device.Attributes == nil {
		return ""
	}
	return firstNonEmptyString(
		device.Attributes["product_id"],
		device.Attributes["productID"],
		device.Attributes["product"],
	)
}

func firmwareVersionForDevice(device *devices.Device) int {
	if device == nil || device.Attributes == nil {
		return 0
	}
	for _, key := range []string{"product_firmware_version", "firmware_version", "firmwareVersion", "version"} {
		version, err := strconv.Atoi(device.Attributes[key])
		if err == nil {
			return version
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intFromString(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}
