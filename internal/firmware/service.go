// Package firmware stores prebuilt binaries and manages OTA flash job state.
package firmware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
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

// ProductDeviceResolver looks up per-device product firmware rollout policy.
type ProductDeviceResolver interface {
	GetByProductID(ctx context.Context, productID string) ([]domain.ProductDevice, error)
}

// FlashTransport is implemented by the TCP layer to send OTA begin/chunk messages.
type FlashTransport interface {
	BeginFlash(ctx context.Context, deviceID string, job *domain.FlashJob) error
	SendFlashChunk(ctx context.Context, deviceID string, job *domain.FlashJob, chunk domain.OTAChunk, data []byte) error
}

// FlashCompletionTransport optionally finalizes OTA with an UpdateDone message.
type FlashCompletionTransport interface {
	CompleteFlash(ctx context.Context, deviceID string, job *domain.FlashJob) error
}

// EventPublisher lets firmware jobs emit spark/flash progress events.
type EventPublisher interface {
	Publish(ctx context.Context, event *domain.Event) (*domain.Event, error)
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
	firmwares       repository.ProductFirmwareRepository
	flashJobs       repository.FlashJobRepository
	productDevices  ProductDeviceResolver
	flashTransport  FlashTransport
	events          EventPublisher
	binaryDirectory string
	chunkSize       int
	clock           func() time.Time
}

// NewService creates a firmware service with optional persistent flash-job storage.
func NewService(
	firmwares       repository.ProductFirmwareRepository,
	binaryDirectory string,
	flashJobs       ...repository.FlashJobRepository,
) *Service {
	var flashJobRepository repository.FlashJobRepository
	if len(flashJobs) > 0 {
		flashJobRepository = flashJobs[0]
	}

	return &Service{
		firmwares:       firmwares,
		flashJobs:       flashJobRepository,
		binaryDirectory: binaryDirectory,
		chunkSize:       DefaultFlashChunkSize,
		clock:           time.Now,
	}
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

// UploadProductFirmware writes the binary file and stores its searchable metadata.
func (service *Service) UploadProductFirmware(
	ctx    context.Context,
	upload Upload,
) (*domain.ProductFirmware, error) {
	if upload.ProductID == "" || upload.Reader == nil {
		return nil, repository.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	version := upload.Version
	if version == 0 {
		nextVersion, err := service.nextVersion(ctx, upload.ProductID)
		if err != nil {
			return nil, err
		}
		version = nextVersion
	}

	now := service.clock().UTC()
	id := newFirmwareID()
	binaryPath, size, checksum, err := service.writeBinary(ctx, id, upload.Filename, upload.Reader)
	if err != nil {
		return nil, err
	}

	firmware := &domain.ProductFirmware{
		ID:           id,
		ProductID:    upload.ProductID,
		Version:      version,
		Title:        upload.Title,
		Description:  upload.Description,
		Filename:     upload.Filename,
		ContentType:  upload.ContentType,
		Size:         size,
		SHA256:       checksum,
		BinaryPath:   binaryPath,
		ReleaseNotes: upload.ReleaseNotes,
		Released:     upload.Released || upload.Current || upload.Default,
		Default:      upload.Default || upload.Current,
		Current:      upload.Current,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if firmware.Title == "" {
		firmware.Title = upload.Filename
	}

	if firmware.Current || firmware.Default {
		if err := service.clearCurrent(ctx, upload.ProductID); err != nil {
			return nil, err
		}
		firmware.Current = true
		firmware.Default = true
		firmware.Released = true
	}

	if service.firmwares == nil {
		return firmware, nil
	}

	if err := service.firmwares.Create(ctx, firmware); err != nil {
		return nil, err
	}
	return firmware, nil
}

// StartDeviceFlash creates and starts an OTA job against the configured transport.
func (service *Service) StartDeviceFlash(
	ctx     context.Context,
	request FlashRequest,
) (*domain.FlashJob, error) {
	if request.DeviceID == "" || request.ProductID == "" {
		return nil, repository.ErrNotFound
	}

	target, err := service.selectFirmware(ctx, request.ProductID, request.FirmwareID)
	if err != nil {
		return nil, err
	}
	if target.BinaryPath == "" {
		return nil, repository.ErrNotFound
	}
	chunks, err := service.buildChunkManifest(ctx, target.BinaryPath)
	if err != nil {
		return nil, err
	}

	now := service.clock().UTC()
	job := &domain.FlashJob{
		ID:              newFirmwareID(),
		DeviceID:        request.DeviceID,
		ProductID:       target.ProductID,
		FirmwareID:      target.ID,
		FirmwareVersion: target.Version,
		BinaryPath:      target.BinaryPath,
		Size:            target.Size,
		SHA256:          target.SHA256,
		ChunkSize:       service.chunkSize,
		ChunkCount:      len(chunks),
		Chunks:          chunks,
		Status:          FlashStatusQueued,
		Progress:        0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if service.flashJobs == nil {
		if service.flashTransport != nil {
			if err := service.flashTransport.BeginFlash(ctx, job.DeviceID, job); err != nil {
				if _, failErr := service.failFlashJob(ctx, job, err.Error()); failErr != nil {
					return nil, failErr
				}
				return job, err
			}
			return service.markFlashJobRunning(ctx, job)
		}
		service.publishFlashEvent(ctx, job, "queued")
		return job, nil
	}
	if err := service.flashJobs.Create(ctx, job); err != nil {
		return nil, err
	}
	service.publishFlashEvent(ctx, job, "queued")
	if service.flashTransport != nil {
		return service.beginAndPumpFlashJob(ctx, job)
	}
	return job, nil
}

func (service *Service) StartFlashJob(ctx context.Context, jobID string) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return service.markFlashJobRunning(ctx, job)
}

func (service *Service) markFlashJobRunning(
	ctx context.Context,
	job *domain.FlashJob,
) (*domain.FlashJob, error) {
	now := service.clock().UTC()
	job.Status = FlashStatusRunning
	job.Error = ""
	job.Progress = progressFor(job)
	job.UpdatedAt = now
	job.StartedAt = &now
	if service.flashJobs != nil {
		if err := service.flashJobs.Save(ctx, job); err != nil {
			return nil, err
		}
	}
	service.publishFlashEvent(ctx, job, "running")
	return job, nil
}

func (service *Service) beginAndPumpFlashJob(
	ctx context.Context,
	job *domain.FlashJob,
) (*domain.FlashJob, error) {
	if err := service.flashTransport.BeginFlash(ctx, job.DeviceID, job); err != nil {
		if _, failErr := service.failFlashJob(ctx, job, err.Error()); failErr != nil {
			return nil, failErr
		}
		return job, err
	}

	running, err := service.markFlashJobRunning(ctx, job)
	if err != nil {
		return nil, err
	}
	return service.PumpFlashJob(ctx, running.ID)
}

func (service *Service) PumpFlashJob(ctx context.Context, jobID string) (*domain.FlashJob, error) {
	for {
		job, err := service.SendNextFlashChunk(ctx, jobID)
		if err != nil {
			return job, err
		}
		if job.Status == FlashStatusCompleted || job.Status == FlashStatusFailed {
			return job, nil
		}
	}
}

func (service *Service) MarkFlashChunkTransferred(
	ctx        context.Context,
	jobID      string,
	chunkIndex int,
) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if chunkIndex < 0 || chunkIndex >= len(job.Chunks) {
		return nil, repository.ErrNotFound
	}

	if job.Status == FlashStatusQueued {
		now := service.clock().UTC()
		job.Status = FlashStatusRunning
		job.StartedAt = &now
	}

	if !job.Chunks[chunkIndex].Transferred {
		job.Chunks[chunkIndex].Transferred = true
		job.Transferred++
	}
	if job.Transferred >= job.ChunkCount && job.ChunkCount > 0 {
		return service.completeFlashJob(ctx, job)
	}

	job.Progress = progressFor(job)
	job.UpdatedAt = service.clock().UTC()
	if service.flashJobs != nil {
		if err := service.flashJobs.Save(ctx, job); err != nil {
			return nil, err
		}
	}
	service.publishFlashEvent(ctx, job, "chunk")
	return job, nil
}

func (service *Service) SendFlashChunk(
	ctx        context.Context,
	jobID      string,
	chunkIndex int,
) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if service.flashTransport == nil {
		return nil, repository.ErrNotFound
	}
	if chunkIndex < 0 || chunkIndex >= len(job.Chunks) {
		return nil, repository.ErrNotFound
	}

	chunk := job.Chunks[chunkIndex]
	data, err := readChunkData(ctx, job.BinaryPath, chunk)
	if err != nil {
		return nil, err
	}

	if job.Status == FlashStatusQueued {
		if _, err := service.markFlashJobRunning(ctx, job); err != nil {
			return nil, err
		}
	}

	if err := service.flashTransport.SendFlashChunk(ctx, job.DeviceID, job, chunk, data); err != nil {
		if _, failErr := service.failFlashJob(ctx, job, err.Error()); failErr != nil {
			return nil, failErr
		}
		return job, err
	}

	updated, err := service.MarkFlashChunkTransferred(ctx, job.ID, chunkIndex)
	if err != nil {
		return nil, err
	}
	if updated.Status == FlashStatusCompleted {
		if err := service.completeDeviceFlash(ctx, updated); err != nil {
			if _, failErr := service.failFlashJob(ctx, updated, err.Error()); failErr != nil {
				return nil, failErr
			}
			return updated, err
		}
	}
	return updated, nil
}

func (service *Service) SendNextFlashChunk(
	ctx   context.Context,
	jobID string,
) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	for _, chunk := range job.Chunks {
		if !chunk.Transferred {
			return service.SendFlashChunk(ctx, jobID, chunk.Index)
		}
	}
	return service.completeFlashJob(ctx, job)
}

func (service *Service) CompleteFlashJob(
	ctx   context.Context,
	jobID string,
) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return service.completeFlashJob(ctx, job)
}

func (service *Service) RetryMissedFlashChunks(
	ctx          context.Context,
	deviceID     string,
	chunkIndexes []int,
) (*domain.FlashJob, error) {
	job, err := service.activeFlashJobForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if len(chunkIndexes) == 0 {
		return job, nil
	}
	if len(chunkIndexes) > MaxMissedFlashChunks {
		return service.failFlashJob(ctx, job, "too many missed OTA chunks")
	}

	indexes := uniqueValidChunkIndexes(chunkIndexes, len(job.Chunks))
	if len(indexes) == 0 {
		return job, nil
	}

	service.publishFlashEvent(ctx, job, "missed")
	current := job
	for _, chunkIndex := range indexes {
		current, err = service.SendFlashChunk(ctx, job.ID, chunkIndex)
		if err != nil {
			return current, err
		}
	}
	return current, nil
}

func (service *Service) AbortDeviceFlash(
	ctx      context.Context,
	deviceID string,
	message  string,
) (*domain.FlashJob, error) {
	job, err := service.activeFlashJobForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if message == "" {
		message = "device aborted flash"
	}
	return service.failFlashJob(ctx, job, message)
}

func (service *Service) FailFlashJob(
	ctx     context.Context,
	jobID   string,
	message string,
) (*domain.FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return service.failFlashJob(ctx, job, message)
}

func (service *Service) failFlashJob(
	ctx     context.Context,
	job     *domain.FlashJob,
	message string,
) (*domain.FlashJob, error) {
	now := service.clock().UTC()
	job.Status = FlashStatusFailed
	job.Error = message
	job.UpdatedAt = now
	job.CompletedAt = &now
	if service.flashJobs != nil {
		if err := service.flashJobs.Save(ctx, job); err != nil {
			return nil, err
		}
	}
	service.publishFlashEvent(ctx, job, "failed")
	return job, nil
}

func (service *Service) ListDeviceFlashJobs(
	ctx      context.Context,
	deviceID string,
) ([]domain.FlashJob, error) {
	if deviceID == "" {
		return nil, repository.ErrNotFound
	}
	if service.flashJobs == nil {
		return []domain.FlashJob{}, nil
	}

	jobs, err := service.flashJobs.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	sort.Slice(jobs, func(left int, right int) bool {
		return jobs[left].CreatedAt.Before(jobs[right].CreatedAt)
	})
	return jobs, nil
}

func (service *Service) GetDeviceFlashJob(
	ctx      context.Context,
	deviceID string,
	jobID    string,
) (*domain.FlashJob, error) {
	if deviceID == "" || jobID == "" || service.flashJobs == nil {
		return nil, repository.ErrNotFound
	}

	job, err := service.flashJobs.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.DeviceID != deviceID {
		return nil, repository.ErrNotFound
	}
	return job, nil
}

func (service *Service) getFlashJobByID(
	ctx   context.Context,
	jobID string,
) (*domain.FlashJob, error) {
	if jobID == "" || service.flashJobs == nil {
		return nil, repository.ErrNotFound
	}
	return service.flashJobs.GetByID(ctx, jobID)
}

func (service *Service) activeFlashJobForDevice(
	ctx      context.Context,
	deviceID string,
) (*domain.FlashJob, error) {
	if deviceID == "" || service.flashJobs == nil {
		return nil, repository.ErrNotFound
	}

	jobs, err := service.flashJobs.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	sort.Slice(jobs, func(left int, right int) bool {
		return jobs[left].UpdatedAt.After(jobs[right].UpdatedAt)
	})

	for index := range jobs {
		if jobs[index].Status == FlashStatusRunning || jobs[index].Status == FlashStatusQueued {
			return &jobs[index], nil
		}
	}
	for index := range jobs {
		if jobs[index].Status == FlashStatusCompleted {
			return &jobs[index], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (service *Service) hasFlashJobForDevice(
	ctx        context.Context,
	deviceID   string,
	firmwareID string,
) bool {
	if service.flashJobs == nil {
		return false
	}
	jobs, err := service.flashJobs.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return false
	}
	for _, job := range jobs {
		if job.FirmwareID != firmwareID {
			continue
		}
		if job.Status == FlashStatusQueued || job.Status == FlashStatusRunning || job.Status == FlashStatusCompleted {
			return true
		}
	}
	return false
}

func (service *Service) hasActiveFlashJobForFirmware(ctx context.Context, firmwareID string) bool {
	if service.flashJobs == nil {
		return false
	}
	jobs, err := service.flashJobs.List(ctx)
	if err != nil {
		return false
	}
	for _, job := range jobs {
		if job.FirmwareID != firmwareID {
			continue
		}
		if job.Status == FlashStatusQueued || job.Status == FlashStatusRunning {
			return true
		}
	}
	return false
}

func (service *Service) ListProductFirmware(
	ctx       context.Context,
	productID string,
) ([]domain.ProductFirmware, error) {
	if productID == "" {
		return nil, repository.ErrNotFound
	}
	if service.firmwares == nil {
		return []domain.ProductFirmware{}, nil
	}

	firmwares, err := service.firmwares.GetByProductID(ctx, productID)
	if err != nil {
		return nil, err
	}

	sort.Slice(firmwares, func(left int, right int) bool {
		if firmwares[left].Version == firmwares[right].Version {
			return firmwares[left].CreatedAt.Before(firmwares[right].CreatedAt)
		}
		return firmwares[left].Version < firmwares[right].Version
	})
	return firmwares, nil
}

func (service *Service) GetProductFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
) (*domain.ProductFirmware, error) {
	if productID == "" || firmwareID == "" || service.firmwares == nil {
		return nil, repository.ErrNotFound
	}

	firmware, err := service.firmwares.GetByID(ctx, firmwareID)
	if err == nil {
		if firmware.ProductID != productID {
			return nil, repository.ErrNotFound
		}
		return firmware, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	version, ok := intFromString(firmwareID)
	if !ok {
		return nil, repository.ErrNotFound
	}
	firmwares, err := service.ListProductFirmware(ctx, productID)
	if err != nil {
		return nil, err
	}
	for index := range firmwares {
		if firmwares[index].Version == version {
			return &firmwares[index], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (service *Service) ReleaseProductFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
) (*domain.ProductFirmware, error) {
	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return nil, err
	}

	firmware.Released = true
	firmware.UpdatedAt = service.clock().UTC()
	if service.firmwares != nil {
		if err := service.firmwares.Save(ctx, firmware); err != nil {
			return nil, err
		}
	}
	return firmware, nil
}

func (service *Service) SetDefaultProductFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
) (*domain.ProductFirmware, error) {
	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return nil, err
	}
	if err := service.clearCurrent(ctx, productID); err != nil {
		return nil, err
	}

	firmware.Released = true
	firmware.Default = true
	firmware.Current = true
	firmware.UpdatedAt = service.clock().UTC()
	if service.firmwares != nil {
		if err := service.firmwares.Save(ctx, firmware); err != nil {
			return nil, err
		}
	}
	return firmware, nil
}

func (service *Service) UpdateProductFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
	update     Update,
) (*domain.ProductFirmware, error) {
	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return nil, err
	}

	if update.Version != nil {
		if *update.Version <= 0 {
			return nil, repository.ErrNotFound
		}
		firmware.Version = *update.Version
	}
	if update.Title != nil {
		firmware.Title = *update.Title
	}
	if update.Description != nil {
		firmware.Description = *update.Description
	}
	if update.ReleaseNotes != nil {
		firmware.ReleaseNotes = *update.ReleaseNotes
	}
	if update.Released != nil {
		firmware.Released = *update.Released
	}
	if update.Default != nil {
		firmware.Default = *update.Default
	}
	if update.Current != nil {
		firmware.Current = *update.Current
	}

	if update.Reader != nil {
		filename := firmware.Filename
		if update.Filename != nil && *update.Filename != "" {
			filename = *update.Filename
		}
		if filename == "" {
			filename = "firmware.bin"
		}

		oldPath := firmware.BinaryPath
		binaryPath, size, checksum, err := service.writeBinary(ctx, firmware.ID, filename, update.Reader)
		if err != nil {
			return nil, err
		}
		if oldPath != "" && oldPath != binaryPath {
			_ = os.Remove(oldPath)
		}

		firmware.Filename = filename
		firmware.BinaryPath = binaryPath
		firmware.Size = size
		firmware.SHA256 = checksum
		if update.ContentType != nil {
			firmware.ContentType = *update.ContentType
		}
	} else {
		if update.Filename != nil {
			firmware.Filename = *update.Filename
		}
		if update.ContentType != nil {
			firmware.ContentType = *update.ContentType
		}
	}

	if (update.Default != nil && *update.Default) || (update.Current != nil && *update.Current) {
		if err := service.clearCurrent(ctx, productID); err != nil {
			return nil, err
		}
		firmware.Released = true
		firmware.Default = true
		firmware.Current = true
	}

	firmware.UpdatedAt = service.clock().UTC()
	if service.firmwares != nil {
		if err := service.firmwares.Save(ctx, firmware); err != nil {
			return nil, err
		}
	}
	return firmware, nil
}

func (service *Service) DeleteProductFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
) error {
	if service.firmwares == nil {
		return repository.ErrNotFound
	}

	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return err
	}
	if service.hasActiveFlashJobForFirmware(ctx, firmware.ID) {
		return repository.ErrConflict
	}

	if err := service.firmwares.Delete(ctx, firmware.ID); err != nil {
		return err
	}
	if firmware.BinaryPath != "" {
		if err := os.Remove(firmware.BinaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (service *Service) CheckProductFirmwareUpdate(
	ctx     context.Context,
	request UpdateCheckRequest,
) (*domain.ProductFirmware, bool, error) {
	if request.ProductID == "" {
		return nil, false, repository.ErrNotFound
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
	ctx    context.Context,
	device *domain.Device,
) (*domain.FlashJob, bool, error) {
	if device == nil || device.ID == "" {
		return nil, false, repository.ErrNotFound
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
	ctx       context.Context,
	productID string,
) (*domain.ProductFirmware, error) {
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
		return nil, repository.ErrNotFound
	}
	return &firmwares[len(firmwares)-1], nil
}

func (service *Service) targetProductFirmware(
	ctx           context.Context,
	productID     string,
	targetVersion *int,
) (*domain.ProductFirmware, error) {
	if targetVersion == nil {
		return service.CurrentProductFirmware(ctx, productID)
	}
	return service.GetProductFirmware(ctx, productID, strconv.Itoa(*targetVersion))
}

func (service *Service) desiredFirmwareVersion(
	ctx       context.Context,
	productID string,
	deviceID  string,
) (*int, error) {
	if service.productDevices == nil || productID == "" || deviceID == "" {
		return nil, nil
	}

	devices, err := service.productDevices.GetByProductID(ctx, productID)
	if err != nil {
		return nil, err
	}
	for index := range devices {
		if devices[index].DeviceID == deviceID {
			return devices[index].DesiredFirmwareVersion, nil
		}
	}
	return nil, nil
}

func (service *Service) selectFirmware(
	ctx        context.Context,
	productID  string,
	firmwareID string,
) (*domain.ProductFirmware, error) {
	if firmwareID != "" {
		return service.GetProductFirmware(ctx, productID, firmwareID)
	}
	return service.CurrentProductFirmware(ctx, productID)
}

func (service *Service) nextVersion(ctx context.Context, productID string) (int, error) {
	firmwares, err := service.ListProductFirmware(ctx, productID)
	if err != nil {
		return 0, err
	}

	version := 0
	for _, firmware := range firmwares {
		if firmware.Version > version {
			version = firmware.Version
		}
	}
	return version + 1, nil
}

func (service *Service) clearCurrent(ctx context.Context, productID string) error {
	if service.firmwares == nil {
		return nil
	}

	firmwares, err := service.firmwares.GetByProductID(ctx, productID)
	if err != nil {
		return err
	}

	for index := range firmwares {
		if !firmwares[index].Current {
			continue
		}
		firmwares[index].Current = false
		firmwares[index].Default = false
		firmwares[index].UpdatedAt = service.clock().UTC()
		if err := service.firmwares.Save(ctx, &firmwares[index]); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) writeBinary(
	ctx      context.Context,
	id       string,
	filename string,
	reader   io.Reader,
) (string, int64, string, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, "", err
	}
	if err := os.MkdirAll(service.binaryDirectory, 0o755); err != nil {
		return "", 0, "", err
	}

	extension := filepath.Ext(filename)
	if extension == "" {
		extension = ".bin"
	}
	path := filepath.Join(service.binaryDirectory, id+extension)
	tempFile, err := os.CreateTemp(service.binaryDirectory, ".tmp-*.bin")
	if err != nil {
		return "", 0, "", err
	}

	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	size, err := io.Copy(tempFile, io.TeeReader(reader, hash))
	if closeErr := tempFile.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, "", err
	}
	if size == 0 {
		return "", 0, "", fmt.Errorf("firmware binary is empty")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", 0, "", err
	}

	cleanup = false
	return path, size, hex.EncodeToString(hash.Sum(nil)), nil
}

func (service *Service) buildChunkManifest(
	ctx  context.Context,
	path string,
) ([]domain.OTAChunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	chunkSize := service.chunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultFlashChunkSize
	}

	buffer := make([]byte, chunkSize)
	chunks := make([]domain.OTAChunk, 0)
	var offset int64
	for {
		read, err := io.ReadFull(file, buffer)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if read > 0 {
			hash := sha256.Sum256(buffer[:read])
			chunks = append(chunks, domain.OTAChunk{
				Index:  len(chunks),
				Offset: offset,
				Size:   read,
				SHA256: hex.EncodeToString(hash[:]),
			})
			offset += int64(read)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
	}

	if len(chunks) == 0 {
		return nil, repository.ErrNotFound
	}
	return chunks, nil
}

func (service *Service) completeFlashJob(
	ctx context.Context,
	job *domain.FlashJob,
) (*domain.FlashJob, error) {
	now := service.clock().UTC()
	for index := range job.Chunks {
		job.Chunks[index].Transferred = true
	}
	job.Transferred = job.ChunkCount
	job.Status = FlashStatusCompleted
	job.Progress = 100
	job.Error = ""
	job.UpdatedAt = now
	job.CompletedAt = &now
	if job.StartedAt == nil {
		job.StartedAt = &now
	}
	if service.flashJobs != nil {
		if err := service.flashJobs.Save(ctx, job); err != nil {
			return nil, err
		}
	}
	service.publishFlashEvent(ctx, job, "completed")
	return job, nil
}

func (service *Service) completeDeviceFlash(ctx context.Context, job *domain.FlashJob) error {
	transport, ok := service.flashTransport.(FlashCompletionTransport)
	if !ok {
		return nil
	}
	return transport.CompleteFlash(ctx, job.DeviceID, job)
}

func (service *Service) publishFlashEvent(ctx context.Context, job *domain.FlashJob, phase string) {
	if service.events == nil || job == nil {
		return
	}

	payload, err := json.Marshal(map[string]any{
		"job_id":           job.ID,
		"firmware_id":      job.FirmwareID,
		"firmware_version": job.FirmwareVersion,
		"status":           job.Status,
		"progress":         job.Progress,
		"transferred":      job.Transferred,
		"chunk_count":      job.ChunkCount,
		"error":            job.Error,
	})
	if err != nil {
		return
	}

	_, _ = service.events.Publish(ctx, &domain.Event{
		Name:      "spark/flash/" + phase,
		Data:      string(payload),
		DeviceID:  job.DeviceID,
		ProductID: job.ProductID,
	})
}

func progressFor(job *domain.FlashJob) int {
	if job.ChunkCount <= 0 {
		return 0
	}
	progress := job.Transferred * 100 / job.ChunkCount
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func uniqueValidChunkIndexes(indexes []int, chunkCount int) []int {
	seen := make(map[int]struct{}, len(indexes))
	valid := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= chunkCount {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		valid = append(valid, index)
	}
	return valid
}

func productIDForDevice(device *domain.Device) string {
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

func firmwareVersionForDevice(device *domain.Device) int {
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

func readChunkData(ctx context.Context, path string, chunk domain.OTAChunk) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chunk.Size <= 0 || chunk.Offset < 0 {
		return nil, repository.ErrNotFound
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data := make([]byte, chunk.Size)
	if _, err := file.ReadAt(data, chunk.Offset); err != nil {
		return nil, err
	}

	hash := sha256.Sum256(data)
	if chunk.SHA256 != "" && chunk.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, fmt.Errorf("chunk checksum mismatch")
	}

	return data, nil
}

func intFromString(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func newFirmwareID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
