package products

import (
	"context"
	"errors"
	"time"

	"sparkserver/internal/devices"
)

var (
	ErrNotFound = errors.New("product record not found")
	ErrConflict = errors.New("product record already exists")
)

type Product struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	OwnerID     string    `json:"owner_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (product Product) GetID() string { return product.ID }

type ProductDevice struct {
	ID                     string    `json:"id"`
	ProductID              string    `json:"product_id"`
	DeviceID               string    `json:"device_id"`
	OwnerID                string    `json:"owner_id,omitempty"`
	Notes                  string    `json:"notes,omitempty"`
	Denied                 bool      `json:"denied,omitempty"`
	Development            bool      `json:"development,omitempty"`
	Quarantined            bool      `json:"quarantined,omitempty"`
	DesiredFirmwareVersion *int      `json:"desired_firmware_version,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (device ProductDevice) GetID() string { return device.ID }

type Store interface {
	Create(ctx context.Context, product *Product) error
	GetByID(ctx context.Context, id string) (*Product, error)
	GetBySlug(ctx context.Context, slug string) (*Product, error)
	Save(ctx context.Context, product *Product) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]Product, error)
}

type DeviceStore interface {
	Create(ctx context.Context, device *ProductDevice) error
	GetByProductID(ctx context.Context, productID string) ([]ProductDevice, error)
	Save(ctx context.Context, device *ProductDevice) error
	Delete(ctx context.Context, id string) error
}

type ClaimedDeviceStore interface {
	GetByID(ctx context.Context, id string) (*devices.Device, error)
	Save(ctx context.Context, device *devices.Device) error
}

type FirmwareCatalog interface {
	HasProductFirmwareVersion(ctx context.Context, productID string, version int) (bool, error)
}
