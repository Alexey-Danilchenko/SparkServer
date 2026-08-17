// Package test verifies JSON file repository behavior.
package jsonfile_test

import (
	"errors"
	"testing"
	"time"

	"sparkserver/internal/auth"
	devicepkg "sparkserver/internal/devices"
	eventpkg "sparkserver/internal/events"
	"sparkserver/internal/firmware"
	jsonfile "sparkserver/internal/jsonfile"
	productpkg "sparkserver/internal/products"
	webhookpkg "sparkserver/internal/webhooks"
)

func TestFileStoreCRUD(t *testing.T) {
	ctx := t.Context()
	users := jsonfile.NewUserRepository(t.TempDir())
	now := time.Now().UTC()

	user := auth.User{
		ID:           "user-1",
		Username:     "admin",
		PasswordHash: "hash",
		Scopes:       []string{"*"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := users.Create(ctx, &user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := users.Create(ctx, &user); !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("create duplicate user error = %v", err)
	}

	loaded, err := users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if loaded.Username != user.Username {
		t.Fatalf("loaded username = %q", loaded.Username)
	}

	loaded.PasswordHash = "new-hash"
	if err := users.Save(ctx, loaded); err != nil {
		t.Fatalf("save user: %v", err)
	}

	saved, err := users.GetByUsername(ctx, user.Username)
	if err != nil {
		t.Fatalf("get user by username: %v", err)
	}
	if saved.PasswordHash != "new-hash" {
		t.Fatalf("saved password hash = %q", saved.PasswordHash)
	}

	allUsers, err := users.List(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(allUsers) != 1 {
		t.Fatalf("user count = %d", len(allUsers))
	}

	if err := users.Delete(ctx, user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := users.GetByID(ctx, user.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("get deleted user error = %v", err)
	}
}

func TestFileRepositoryLookups(t *testing.T) {
	ctx := t.Context()

	tokens := jsonfile.NewAccessTokenRepository(t.TempDir())
	if err := tokens.Create(ctx, &auth.AccessToken{Token: "token-1", UserID: "user-1"}); err != nil {
		t.Fatalf("create token 1: %v", err)
	}
	if err := tokens.Create(ctx, &auth.AccessToken{Token: "token-2", UserID: "user-2"}); err != nil {
		t.Fatalf("create token 2: %v", err)
	}

	userTokens, err := tokens.GetByUserID(ctx, "user-1")
	if err != nil {
		t.Fatalf("get tokens by user: %v", err)
	}
	if len(userTokens) != 1 || userTokens[0].Token != "token-1" {
		t.Fatalf("user tokens = %#v", userTokens)
	}

	devices := jsonfile.NewDeviceRepository(t.TempDir())
	if err := devices.Create(ctx, &devicepkg.Device{ID: "device-1", OwnerID: "user-1", Name: "kettle"}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	device, err := devices.GetByName(ctx, "user-1", "kettle")
	if err != nil {
		t.Fatalf("get device by name: %v", err)
	}
	if device.ID != "device-1" {
		t.Fatalf("device id = %q", device.ID)
	}

	products := jsonfile.NewProductRepository(t.TempDir())
	if err := products.Create(ctx, &productpkg.Product{ID: "product-1", Slug: "brew-controller", Name: "Brew Controller"}); err != nil {
		t.Fatalf("create product: %v", err)
	}
	product, err := products.GetBySlug(ctx, "brew-controller")
	if err != nil {
		t.Fatalf("get product by slug: %v", err)
	}
	if product.ID != "product-1" {
		t.Fatalf("product id = %q", product.ID)
	}

	productDevices := jsonfile.NewProductDeviceRepository(t.TempDir())
	if err := productDevices.Create(ctx, &productpkg.ProductDevice{ID: "product-device-1", ProductID: "product-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("create product device: %v", err)
	}
	devicesForProduct, err := productDevices.GetByProductID(ctx, "product-1")
	if err != nil {
		t.Fatalf("get product devices: %v", err)
	}
	if len(devicesForProduct) != 1 || devicesForProduct[0].DeviceID != "device-1" {
		t.Fatalf("product devices = %#v", devicesForProduct)
	}

	firmwares := jsonfile.NewProductFirmwareRepository(t.TempDir())
	if err := firmwares.Create(ctx, &firmware.ProductFirmware{ID: "firmware-1", ProductID: "product-1", Version: 1}); err != nil {
		t.Fatalf("create firmware: %v", err)
	}
	productFirmwares, err := firmwares.GetByProductID(ctx, "product-1")
	if err != nil {
		t.Fatalf("get product firmwares: %v", err)
	}
	if len(productFirmwares) != 1 || productFirmwares[0].Version != 1 {
		t.Fatalf("product firmwares = %#v", productFirmwares)
	}

	webhooks := jsonfile.NewWebhookRepository(t.TempDir())
	if err := webhooks.Create(ctx, &webhookpkg.Webhook{ID: "webhook-1", Event: "brew.started", URL: "https://example.test/hook"}); err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	webhooksForEvent, err := webhooks.GetByEvent(ctx, "brew.started")
	if err != nil {
		t.Fatalf("get webhooks by event: %v", err)
	}
	if len(webhooksForEvent) != 1 || webhooksForEvent[0].ID != "webhook-1" {
		t.Fatalf("webhooks = %#v", webhooksForEvent)
	}

	events := jsonfile.NewEventRepository(t.TempDir())
	if err := events.Create(ctx, &eventpkg.Event{ID: "event-1", Name: "brew.started"}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := events.Create(ctx, &eventpkg.Event{ID: "event-2", Name: "device.online"}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	brewEvents, err := events.GetByNamePrefix(ctx, "brew.")
	if err != nil {
		t.Fatalf("get events by prefix: %v", err)
	}
	if len(brewEvents) != 1 || brewEvents[0].ID != "event-1" {
		t.Fatalf("brew events = %#v", brewEvents)
	}
}

func TestFileStoreRejectsPathLikeIDs(t *testing.T) {
	ctx := t.Context()
	users := jsonfile.NewUserRepository(t.TempDir())
	user := auth.User{ID: "../outside", Username: "bad"}

	if err := users.Create(ctx, &user); err == nil {
		t.Fatal("expected invalid id error")
	}
}
