// Package test verifies product service behavior independently of HTTP routing.
package products_test

import (
	"errors"
	"path/filepath"
	"testing"

	"sparkserver/internal/devices"
	"sparkserver/internal/firmware"
	jsonfile "sparkserver/internal/jsonfile"
	"sparkserver/internal/products"
)

func TestProductDeviceDesiredFirmwareVersionRequiresExistingFirmware(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	deviceRepository := jsonfile.NewDeviceRepository(filepath.Join(dir, "devices"))
	firmwareRepository := jsonfile.NewProductFirmwareRepository(filepath.Join(dir, "firmware"))
	service := products.NewService(
		jsonfile.NewProductRepository(filepath.Join(dir, "products")),
		jsonfile.NewProductDeviceRepository(filepath.Join(dir, "productDevices")),
		deviceRepository,
		products.WithFirmwareCatalog(firmwareRepository),
	)

	product, err := service.Create(ctx, products.CreateRequest{
		OwnerID: "owner-1",
		ID:      "product-1",
		Slug:    "product-1",
		Name:    "Product 1",
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := deviceRepository.Create(ctx, &devices.Device{
		ID:      "device-1",
		OwnerID: "owner-1",
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := service.AddDevice(ctx, "owner-1", product.ID, "device-1"); err != nil {
		t.Fatalf("add product device: %v", err)
	}
	if err := firmwareRepository.Create(ctx, &firmware.ProductFirmware{
		ID:        "firmware-2",
		ProductID: product.ID,
		Version:   2,
	}); err != nil {
		t.Fatalf("create firmware: %v", err)
	}

	missingVersion := 3
	if _, err := service.UpdateDevice(ctx, "owner-1", product.ID, "device-1", products.ProductDeviceUpdateRequest{
		DesiredFirmwareVersion: &missingVersion,
	}); !errors.Is(err, products.ErrNotFound) {
		t.Fatalf("missing desired firmware err = %v", err)
	}

	existingVersion := 2
	link, err := service.UpdateDevice(ctx, "owner-1", product.ID, "device-1", products.ProductDeviceUpdateRequest{
		DesiredFirmwareVersion: &existingVersion,
	})
	if err != nil {
		t.Fatalf("set desired firmware: %v", err)
	}
	if link.DesiredFirmwareVersion == nil || *link.DesiredFirmwareVersion != existingVersion {
		t.Fatalf("desired firmware version = %#v", link.DesiredFirmwareVersion)
	}
}
