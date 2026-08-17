package firmware

import (
	"context"
	"errors"
	"time"

	"sparkserver/internal/devices"
	"sparkserver/internal/events"
)

var (
	ErrNotFound = errors.New("firmware record not found")
	ErrConflict = errors.New("firmware record already exists")
)

type ProductFirmware struct {
	ID           string    `json:"id"`
	ProductID    string    `json:"product_id"`
	Version      int       `json:"version"`
	Title        string    `json:"title,omitempty"`
	Description  string    `json:"description,omitempty"`
	Filename     string    `json:"filename,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	Size         int64     `json:"size,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	BinaryPath   string    `json:"binary_path,omitempty"`
	ReleaseNotes string    `json:"release_notes,omitempty"`
	Released     bool      `json:"released"`
	Default      bool      `json:"default"`
	Current      bool      `json:"current"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (firmware ProductFirmware) GetID() string { return firmware.ID }

type FlashJob struct {
	ID              string     `json:"id"`
	DeviceID        string     `json:"device_id"`
	ProductID       string     `json:"product_id"`
	FirmwareID      string     `json:"firmware_id"`
	FirmwareVersion int        `json:"firmware_version"`
	BinaryPath      string     `json:"binary_path"`
	Size            int64      `json:"size"`
	SHA256          string     `json:"sha256"`
	ChunkSize       int        `json:"chunk_size"`
	ChunkCount      int        `json:"chunk_count"`
	Transferred     int        `json:"transferred_chunks"`
	Chunks          []OTAChunk `json:"chunks,omitempty"`
	Status          string     `json:"status"`
	Progress        int        `json:"progress"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

func (job FlashJob) GetID() string { return job.ID }

type OTAChunk struct {
	Index       int    `json:"index"`
	Offset      int64  `json:"offset"`
	Size        int    `json:"size"`
	SHA256      string `json:"sha256"`
	Transferred bool   `json:"transferred"`
}

type Store interface {
	Create(ctx context.Context, firmware *ProductFirmware) error
	GetByID(ctx context.Context, id string) (*ProductFirmware, error)
	Save(ctx context.Context, firmware *ProductFirmware) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]ProductFirmware, error)
	GetByProductID(ctx context.Context, productID string) ([]ProductFirmware, error)
}

type FlashJobStore interface {
	Create(ctx context.Context, job *FlashJob) error
	GetByID(ctx context.Context, id string) (*FlashJob, error)
	Save(ctx context.Context, job *FlashJob) error
	List(ctx context.Context) ([]FlashJob, error)
	GetByDeviceID(ctx context.Context, deviceID string) ([]FlashJob, error)
}

type ProductDeviceResolver interface {
	DesiredFirmwareVersion(ctx context.Context, productID string, deviceID string) (*int, error)
}

type FlashTransport interface {
	BeginFlash(ctx context.Context, deviceID string, job *FlashJob) error
	SendFlashChunk(ctx context.Context, deviceID string, job *FlashJob, chunk OTAChunk, data []byte) error
}

type FlashCompletionTransport interface {
	CompleteFlash(ctx context.Context, deviceID string, job *FlashJob) error
}

type EventPublisher interface {
	Publish(ctx context.Context, event *events.Event) (*events.Event, error)
}

type DeviceFirmwareUpdater interface {
	CheckAndStartProductFirmwareUpdate(ctx context.Context, device *devices.Device) (*FlashJob, bool, error)
}
