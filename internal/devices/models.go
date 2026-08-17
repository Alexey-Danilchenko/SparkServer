package devices

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("device record not found")
	ErrConflict      = errors.New("device record already exists")
	ErrDeviceOffline = errors.New("device is offline")
	ErrDeviceTimeout = errors.New("device request timed out")
)

type Device struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	OwnerID     string            `json:"owner_id,omitempty"`
	ProductID   string            `json:"product_id,omitempty"`
	Connected   bool              `json:"connected"`
	Variables   map[string]string `json:"variables,omitempty"`
	Functions   []string          `json:"functions,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	LastHeardAt *time.Time        `json:"last_heard_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (device Device) GetID() string { return device.ID }

type DeviceKey struct {
	DeviceID  string    `json:"device_id"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (key DeviceKey) GetID() string { return key.DeviceID }

type DeviceClaim struct {
	Code      string     `json:"code"`
	OwnerID   string     `json:"owner_id"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	DeviceID  string     `json:"device_id,omitempty"`
}

func (claim DeviceClaim) GetID() string { return claim.Code }

type Description struct {
	Variables  map[string]string `json:"variables,omitempty"`
	Functions  []string          `json:"functions,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Store interface {
	Create(ctx context.Context, device *Device) error
	GetByID(ctx context.Context, id string) (*Device, error)
	GetByName(ctx context.Context, ownerID string, name string) (*Device, error)
	Save(ctx context.Context, device *Device) error
	List(ctx context.Context) ([]Device, error)
}

type ClaimStore interface {
	Create(ctx context.Context, claim *DeviceClaim) error
	GetByID(ctx context.Context, id string) (*DeviceClaim, error)
	Save(ctx context.Context, claim *DeviceClaim) error
}
