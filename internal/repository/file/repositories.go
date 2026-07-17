// Package file contains concrete repository adapters over the generic JSON store.
package file

import (
	"context"
	"strings"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
)

// UserRepository implements repository.UserRepository over JSON files.
type UserRepository struct {
	*Store[domain.User]
}

func NewUserRepository(directory string) *UserRepository {
	return &UserRepository{Store: NewStore[domain.User](directory)}
}

func (repo *UserRepository) GetByUsername(
	ctx      context.Context,
	username string,
) (*domain.User, error) {
	users, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range users {
		if users[index].Username == username {
			return &users[index], nil
		}
	}

	return nil, repository.ErrNotFound
}

// AccessTokenRepository implements repository.AccessTokenRepository over JSON files.
type AccessTokenRepository struct {
	*Store[domain.AccessToken]
}

func NewAccessTokenRepository(directory string) *AccessTokenRepository {
	return &AccessTokenRepository{Store: NewStore[domain.AccessToken](directory)}
}

func (repo *AccessTokenRepository) GetByUserID(
	ctx    context.Context,
	userID string,
) ([]domain.AccessToken, error) {
	tokens, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.AccessToken, 0)
	for _, token := range tokens {
		if token.UserID == userID {
			matches = append(matches, token)
		}
	}

	return matches, nil
}

// DeviceRepository implements repository.DeviceRepository over JSON files.
type DeviceRepository struct {
	*Store[domain.Device]
}

func NewDeviceRepository(directory string) *DeviceRepository {
	return &DeviceRepository{Store: NewStore[domain.Device](directory)}
}

func (repo *DeviceRepository) GetByName(
	ctx     context.Context,
	ownerID string,
	name    string,
) (*domain.Device, error) {
	devices, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range devices {
		if devices[index].OwnerID == ownerID && devices[index].Name == name {
			return &devices[index], nil
		}
	}

	return nil, repository.ErrNotFound
}

type DeviceKeyRepository struct {
	*Store[domain.DeviceKey]
}

func NewDeviceKeyRepository(directory string) *DeviceKeyRepository {
	return &DeviceKeyRepository{Store: NewStore[domain.DeviceKey](directory)}
}

type DeviceClaimRepository struct {
	*Store[domain.DeviceClaim]
}

func NewDeviceClaimRepository(directory string) *DeviceClaimRepository {
	return &DeviceClaimRepository{Store: NewStore[domain.DeviceClaim](directory)}
}

type ProductRepository struct {
	*Store[domain.Product]
}

func NewProductRepository(directory string) *ProductRepository {
	return &ProductRepository{Store: NewStore[domain.Product](directory)}
}

func (repo *ProductRepository) GetBySlug(
	ctx  context.Context,
	slug string,
) (*domain.Product, error) {
	products, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	for index := range products {
		if products[index].Slug == slug {
			return &products[index], nil
		}
	}

	return nil, repository.ErrNotFound
}

type ProductDeviceRepository struct {
	*Store[domain.ProductDevice]
}

func NewProductDeviceRepository(directory string) *ProductDeviceRepository {
	return &ProductDeviceRepository{Store: NewStore[domain.ProductDevice](directory)}
}

func (repo *ProductDeviceRepository) GetByProductID(
	ctx       context.Context,
	productID string,
) ([]domain.ProductDevice, error) {
	devices, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.ProductDevice, 0)
	for _, device := range devices {
		if device.ProductID == productID {
			matches = append(matches, device)
		}
	}

	return matches, nil
}

// ProductFirmwareRepository stores uploaded firmware metadata, not binary contents.
type ProductFirmwareRepository struct {
	*Store[domain.ProductFirmware]
}

func NewProductFirmwareRepository(directory string) *ProductFirmwareRepository {
	return &ProductFirmwareRepository{Store: NewStore[domain.ProductFirmware](directory)}
}

func (repo *ProductFirmwareRepository) GetByProductID(
	ctx       context.Context,
	productID string,
) ([]domain.ProductFirmware, error) {
	firmwares, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.ProductFirmware, 0)
	for _, firmware := range firmwares {
		if firmware.ProductID == productID {
			matches = append(matches, firmware)
		}
	}

	return matches, nil
}

type FlashJobRepository struct {
	*Store[domain.FlashJob]
}

func NewFlashJobRepository(directory string) *FlashJobRepository {
	return &FlashJobRepository{Store: NewStore[domain.FlashJob](directory)}
}

func (repo *FlashJobRepository) GetByDeviceID(
	ctx      context.Context,
	deviceID string,
) ([]domain.FlashJob, error) {
	jobs, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.FlashJob, 0)
	for _, job := range jobs {
		if job.DeviceID == deviceID {
			matches = append(matches, job)
		}
	}

	return matches, nil
}

type WebhookRepository struct {
	*Store[domain.Webhook]
}

func NewWebhookRepository(directory string) *WebhookRepository {
	return &WebhookRepository{Store: NewStore[domain.Webhook](directory)}
}

func (repo *WebhookRepository) GetByEvent(
	ctx       context.Context,
	eventName string,
) ([]domain.Webhook, error) {
	webhooks, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.Webhook, 0)
	for _, webhook := range webhooks {
		if webhook.Event == eventName {
			matches = append(matches, webhook)
		}
	}

	return matches, nil
}

type EventRepository struct {
	*Store[domain.Event]
}

func NewEventRepository(directory string) *EventRepository {
	return &EventRepository{Store: NewStore[domain.Event](directory)}
}

func (repo *EventRepository) GetByNamePrefix(
	ctx    context.Context,
	prefix string,
) ([]domain.Event, error) {
	events, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.Event, 0)
	for _, event := range events {
		if strings.HasPrefix(event.Name, prefix) {
			matches = append(matches, event)
		}
	}

	return matches, nil
}
