// Package repository defines storage contracts used by domain services.
package repository

import (
	"context"
	"errors"

	"sparkserver/internal/domain"
)

var (
	// ErrConflict indicates a create request would overwrite an existing record.
	ErrConflict = errors.New("record already exists")
	// ErrNotFound indicates a requested record or scoped resource does not exist.
	ErrNotFound = errors.New("record not found")
)

// Record is the minimum identity contract required by generic repositories.
type Record interface {
	GetID() string
}

// Repository is the CRUD surface shared by file and future Mongo implementations.
type Repository[T Record] interface {
	Create(ctx context.Context, record *T) error
	GetByID(ctx context.Context, id string) (*T, error)
	Save(ctx context.Context, record *T) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]T, error)
}

// UserRepository adds username lookup needed by password login.
type UserRepository interface {
	Repository[domain.User]
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
}

// AccessTokenRepository adds user-scoped token listing for local account management.
type AccessTokenRepository interface {
	Repository[domain.AccessToken]
	GetByUserID(ctx context.Context, userID string) ([]domain.AccessToken, error)
}

// DeviceRepository adds owner/name lookup used by Particle-style device routes.
type DeviceRepository interface {
	Repository[domain.Device]
	GetByName(ctx context.Context, ownerID string, name string) (*domain.Device, error)
}

type DeviceKeyRepository interface {
	Repository[domain.DeviceKey]
}

type DeviceClaimRepository interface {
	Repository[domain.DeviceClaim]
}

// ProductRepository adds slug lookup for product/fleet routes.
type ProductRepository interface {
	Repository[domain.Product]
	GetBySlug(ctx context.Context, slug string) (*domain.Product, error)
}

// ProductDeviceRepository lists devices assigned to a product.
type ProductDeviceRepository interface {
	Repository[domain.ProductDevice]
	GetByProductID(ctx context.Context, productID string) ([]domain.ProductDevice, error)
}

// ProductFirmwareRepository lists uploaded binaries for a product.
type ProductFirmwareRepository interface {
	Repository[domain.ProductFirmware]
	GetByProductID(ctx context.Context, productID string) ([]domain.ProductFirmware, error)
}

// FlashJobRepository lists OTA jobs for operator and API status views.
type FlashJobRepository interface {
	Repository[domain.FlashJob]
	GetByDeviceID(ctx context.Context, deviceID string) ([]domain.FlashJob, error)
}

// WebhookRepository stores event subscriptions and delivery state.
type WebhookRepository interface {
	Repository[domain.Webhook]
	GetByEvent(ctx context.Context, eventName string) ([]domain.Webhook, error)
}

// EventRepository stores published events for local inspection and future replay.
type EventRepository interface {
	Repository[domain.Event]
	GetByNamePrefix(ctx context.Context, prefix string) ([]domain.Event, error)
}
