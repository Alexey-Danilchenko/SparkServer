// Package jsonfile contains feature-specific persistence adapters over a private
// generic JSON store.
package jsonfile

import (
	"context"
	"strings"

	authpkg "sparkserver/internal/auth"
	devicepkg "sparkserver/internal/devices"
	eventpkg "sparkserver/internal/events"
	firmwarepkg "sparkserver/internal/firmware"
	productpkg "sparkserver/internal/products"
	webhookpkg "sparkserver/internal/webhooks"
)

// UserRepository stores auth users as JSON files.
type UserRepository struct {
	*store[authpkg.User]
}

func NewUserRepository(directory string) *UserRepository {
	return &UserRepository{store: newStore[authpkg.User](directory, authpkg.ErrNotFound, authpkg.ErrConflict)}
}

func (repo *UserRepository) GetByUsername(
	ctx context.Context,
	username string,
) (*authpkg.User, error) {
	users, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range users {
		if users[index].Username == username {
			return &users[index], nil
		}
	}

	return nil, authpkg.ErrNotFound
}

// AccessTokenRepository stores auth tokens as JSON files.
type AccessTokenRepository struct {
	*store[authpkg.AccessToken]
}

func NewAccessTokenRepository(directory string) *AccessTokenRepository {
	return &AccessTokenRepository{store: newStore[authpkg.AccessToken](directory, authpkg.ErrNotFound, authpkg.ErrConflict)}
}

func (repo *AccessTokenRepository) GetByUserID(
	ctx context.Context,
	userID string,
) ([]authpkg.AccessToken, error) {
	tokens, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]authpkg.AccessToken, 0)
	for _, token := range tokens {
		if token.UserID == userID {
			matches = append(matches, token)
		}
	}

	return matches, nil
}

// DeviceRepository stores claimed devices as JSON files.
type DeviceRepository struct {
	*store[devicepkg.Device]
}

func NewDeviceRepository(directory string) *DeviceRepository {
	return &DeviceRepository{store: newStore[devicepkg.Device](directory, devicepkg.ErrNotFound, devicepkg.ErrConflict)}
}

func (repo *DeviceRepository) GetByName(
	ctx context.Context,
	ownerID string,
	name string,
) (*devicepkg.Device, error) {
	devices, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range devices {
		if devices[index].OwnerID == ownerID && devices[index].Name == name {
			return &devices[index], nil
		}
	}

	return nil, devicepkg.ErrNotFound
}

type DeviceKeyRepository struct {
	*store[devicepkg.DeviceKey]
}

func NewDeviceKeyRepository(directory string) *DeviceKeyRepository {
	return &DeviceKeyRepository{store: newStore[devicepkg.DeviceKey](directory, devicepkg.ErrNotFound, devicepkg.ErrConflict)}
}

type DeviceClaimRepository struct {
	*store[devicepkg.DeviceClaim]
}

func NewDeviceClaimRepository(directory string) *DeviceClaimRepository {
	return &DeviceClaimRepository{store: newStore[devicepkg.DeviceClaim](directory, devicepkg.ErrNotFound, devicepkg.ErrConflict)}
}

type ProductRepository struct {
	*store[productpkg.Product]
}

func NewProductRepository(directory string) *ProductRepository {
	return &ProductRepository{store: newStore[productpkg.Product](directory, productpkg.ErrNotFound, productpkg.ErrConflict)}
}

func (repo *ProductRepository) GetBySlug(
	ctx context.Context,
	slug string,
) (*productpkg.Product, error) {
	products, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range products {
		if products[index].Slug == slug {
			return &products[index], nil
		}
	}

	return nil, productpkg.ErrNotFound
}

type ProductDeviceRepository struct {
	*store[productpkg.ProductDevice]
}

func NewProductDeviceRepository(directory string) *ProductDeviceRepository {
	return &ProductDeviceRepository{store: newStore[productpkg.ProductDevice](directory, productpkg.ErrNotFound, productpkg.ErrConflict)}
}

func (repo *ProductDeviceRepository) DesiredFirmwareVersion(
	ctx context.Context,
	productID string,
	deviceID string,
) (*int, error) {
	productDevices, err := repo.GetByProductID(ctx, productID)
	if err != nil {
		return nil, err
	}
	for index := range productDevices {
		if productDevices[index].DeviceID == deviceID {
			return productDevices[index].DesiredFirmwareVersion, nil
		}
	}
	return nil, nil
}

func (repo *ProductDeviceRepository) GetByProductID(
	ctx context.Context,
	productID string,
) ([]productpkg.ProductDevice, error) {
	devices, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]productpkg.ProductDevice, 0)
	for _, device := range devices {
		if device.ProductID == productID {
			matches = append(matches, device)
		}
	}

	return matches, nil
}

// ProductFirmwareRepository stores uploaded firmware metadata, not binary contents.
type ProductFirmwareRepository struct {
	*store[firmwarepkg.ProductFirmware]
}

func NewProductFirmwareRepository(directory string) *ProductFirmwareRepository {
	return &ProductFirmwareRepository{store: newStore[firmwarepkg.ProductFirmware](directory, firmwarepkg.ErrNotFound, firmwarepkg.ErrConflict)}
}

func (repo *ProductFirmwareRepository) HasProductFirmwareVersion(
	ctx context.Context,
	productID string,
	version int,
) (bool, error) {
	firmwares, err := repo.GetByProductID(ctx, productID)
	if err != nil {
		return false, err
	}
	for index := range firmwares {
		if firmwares[index].Version == version {
			return true, nil
		}
	}
	return false, nil
}

func (repo *ProductFirmwareRepository) GetByProductID(
	ctx context.Context,
	productID string,
) ([]firmwarepkg.ProductFirmware, error) {
	firmwares, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]firmwarepkg.ProductFirmware, 0)
	for _, firmware := range firmwares {
		if firmware.ProductID == productID {
			matches = append(matches, firmware)
		}
	}

	return matches, nil
}

type FlashJobRepository struct {
	*store[firmwarepkg.FlashJob]
}

func NewFlashJobRepository(directory string) *FlashJobRepository {
	return &FlashJobRepository{store: newStore[firmwarepkg.FlashJob](directory, firmwarepkg.ErrNotFound, firmwarepkg.ErrConflict)}
}

func (repo *FlashJobRepository) GetByDeviceID(
	ctx context.Context,
	deviceID string,
) ([]firmwarepkg.FlashJob, error) {
	jobs, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]firmwarepkg.FlashJob, 0)
	for _, job := range jobs {
		if job.DeviceID == deviceID {
			matches = append(matches, job)
		}
	}

	return matches, nil
}

type WebhookRepository struct {
	*store[webhookpkg.Webhook]
}

func NewWebhookRepository(directory string) *WebhookRepository {
	return &WebhookRepository{store: newStore[webhookpkg.Webhook](directory, webhookpkg.ErrNotFound, webhookpkg.ErrConflict)}
}

func (repo *WebhookRepository) GetByEvent(
	ctx context.Context,
	eventName string,
) ([]webhookpkg.Webhook, error) {
	webhooks, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]webhookpkg.Webhook, 0)
	for _, webhook := range webhooks {
		if webhook.Event == eventName {
			matches = append(matches, webhook)
		}
	}

	return matches, nil
}

type EventRepository struct {
	*store[eventpkg.Event]
}

func NewEventRepository(directory string) *EventRepository {
	return &EventRepository{store: newStore[eventpkg.Event](directory, eventpkg.ErrNotFound, eventpkg.ErrConflict)}
}

func (repo *EventRepository) GetByNamePrefix(
	ctx context.Context,
	prefix string,
) ([]eventpkg.Event, error) {
	events, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]eventpkg.Event, 0)
	for _, event := range events {
		if strings.HasPrefix(event.Name, prefix) {
			matches = append(matches, event)
		}
	}

	return matches, nil
}
