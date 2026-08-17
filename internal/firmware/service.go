package firmware

import (
	"io"
	"time"
)

// Upload describes a prebuilt firmware binary and its product metadata.
type Upload struct {
	ProductID    string
	Version      int
	Title        string
	Description  string
	Filename     string
	ContentType  string
	ReleaseNotes string
	Released     bool
	Default      bool
	Current      bool
	Reader       io.Reader
}

// Update describes mutable firmware metadata plus an optional replacement binary.
type Update struct {
	Version      *int
	Title        *string
	Description  *string
	Filename     *string
	ContentType  *string
	ReleaseNotes *string
	Released     *bool
	Default      *bool
	Current      *bool
	Reader       io.Reader
}

// FlashRequest selects a product firmware image for one connected device.
type FlashRequest struct {
	DeviceID   string
	ProductID  string
	FirmwareID string
}

// UpdateCheckRequest models a device product-firmware version check.
type UpdateCheckRequest struct {
	DeviceID        string
	ProductID       string
	TargetVersion   *int
	CurrentVersion  int
	CurrentFirmware string
}

const (
	DefaultFlashChunkSize = 512
	MaxMissedFlashChunks  = 10

	FlashStatusQueued    = "queued"
	FlashStatusRunning   = "running"
	FlashStatusCompleted = "completed"
	FlashStatusFailed    = "failed"
)

// Service owns firmware metadata, binary files, and OTA job transitions.
type Service struct {
	firmwares       Store
	flashJobs       FlashJobStore
	productDevices  ProductDeviceResolver
	flashTransport  FlashTransport
	events          EventPublisher
	binaryDirectory string
	chunkSize       int
	clock           func() time.Time
}

type Option func(*Service)

func WithEventPublisher(publisher EventPublisher) Option {
	return func(service *Service) { service.events = publisher }
}

func WithProductDeviceResolver(resolver ProductDeviceResolver) Option {
	return func(service *Service) { service.productDevices = resolver }
}

func WithFlashChunkSize(chunkSize int) Option {
	return func(service *Service) {
		if chunkSize > 0 {
			service.chunkSize = chunkSize
		}
	}
}

// NewService creates a firmware service with optional persistent flash-job storage.
func NewService(
	firmwares Store,
	binaryDirectory string,
	flashJobs FlashJobStore,
	options ...Option,
) *Service {
	service := &Service{
		firmwares:       firmwares,
		flashJobs:       flashJobs,
		binaryDirectory: binaryDirectory,
		chunkSize:       DefaultFlashChunkSize,
		clock:           time.Now,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *Service) SetFlashChunkSize(chunkSize int) {
	if chunkSize > 0 {
		service.chunkSize = chunkSize
	}
}

func (service *Service) SetFlashTransport(transport FlashTransport) {
	service.flashTransport = transport
}

func (service *Service) SetEventPublisher(events EventPublisher) {
	service.events = events
}

// SetProductDeviceResolver enables Brewskey-style per-device firmware locks.
func (service *Service) SetProductDeviceResolver(resolver ProductDeviceResolver) {
	service.productDevices = resolver
}
