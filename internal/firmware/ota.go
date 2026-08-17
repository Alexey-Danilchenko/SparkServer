package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"sparkserver/internal/events"
)

// StartDeviceFlash creates and starts an OTA job against the configured transport.
func (service *Service) StartDeviceFlash(
	ctx context.Context,
	request FlashRequest,
) (*FlashJob, error) {
	if request.DeviceID == "" || request.ProductID == "" {
		return nil, ErrNotFound
	}

	target, err := service.selectFirmware(ctx, request.ProductID, request.FirmwareID)
	if err != nil {
		return nil, err
	}
	if target.BinaryPath == "" {
		return nil, ErrNotFound
	}
	chunks, err := service.buildChunkManifest(ctx, target.BinaryPath)
	if err != nil {
		return nil, err
	}

	now := service.clock().UTC()
	job := &FlashJob{
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

func (service *Service) StartFlashJob(ctx context.Context, jobID string) (*FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return service.markFlashJobRunning(ctx, job)
}

func (service *Service) markFlashJobRunning(
	ctx context.Context,
	job *FlashJob,
) (*FlashJob, error) {
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
	job *FlashJob,
) (*FlashJob, error) {
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

func (service *Service) PumpFlashJob(ctx context.Context, jobID string) (*FlashJob, error) {
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
	ctx context.Context,
	jobID string,
	chunkIndex int,
) (*FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if chunkIndex < 0 || chunkIndex >= len(job.Chunks) {
		return nil, ErrNotFound
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
	ctx context.Context,
	jobID string,
	chunkIndex int,
) (*FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if service.flashTransport == nil {
		return nil, ErrNotFound
	}
	if chunkIndex < 0 || chunkIndex >= len(job.Chunks) {
		return nil, ErrNotFound
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
	ctx context.Context,
	jobID string,
) (*FlashJob, error) {
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
	ctx context.Context,
	jobID string,
) (*FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return service.completeFlashJob(ctx, job)
}

func (service *Service) RetryMissedFlashChunks(
	ctx context.Context,
	deviceID string,
	chunkIndexes []int,
) (*FlashJob, error) {
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
	ctx context.Context,
	deviceID string,
	message string,
) (*FlashJob, error) {
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
	ctx context.Context,
	jobID string,
	message string,
) (*FlashJob, error) {
	job, err := service.getFlashJobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return service.failFlashJob(ctx, job, message)
}

func (service *Service) failFlashJob(
	ctx context.Context,
	job *FlashJob,
	message string,
) (*FlashJob, error) {
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
	ctx context.Context,
	deviceID string,
) ([]FlashJob, error) {
	if deviceID == "" {
		return nil, ErrNotFound
	}
	if service.flashJobs == nil {
		return []FlashJob{}, nil
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
	ctx context.Context,
	deviceID string,
	jobID string,
) (*FlashJob, error) {
	if deviceID == "" || jobID == "" || service.flashJobs == nil {
		return nil, ErrNotFound
	}

	job, err := service.flashJobs.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.DeviceID != deviceID {
		return nil, ErrNotFound
	}
	return job, nil
}

func (service *Service) getFlashJobByID(
	ctx context.Context,
	jobID string,
) (*FlashJob, error) {
	if jobID == "" || service.flashJobs == nil {
		return nil, ErrNotFound
	}
	return service.flashJobs.GetByID(ctx, jobID)
}

func (service *Service) activeFlashJobForDevice(
	ctx context.Context,
	deviceID string,
) (*FlashJob, error) {
	if deviceID == "" || service.flashJobs == nil {
		return nil, ErrNotFound
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
	return nil, ErrNotFound
}

func (service *Service) hasFlashJobForDevice(
	ctx context.Context,
	deviceID string,
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

func (service *Service) buildChunkManifest(
	ctx context.Context,
	path string,
) ([]OTAChunk, error) {
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
	chunks := make([]OTAChunk, 0)
	var offset int64
	for {
		read, err := io.ReadFull(file, buffer)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		if read > 0 {
			hash := sha256.Sum256(buffer[:read])
			chunks = append(chunks, OTAChunk{
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
		return nil, ErrNotFound
	}
	return chunks, nil
}

func (service *Service) completeFlashJob(
	ctx context.Context,
	job *FlashJob,
) (*FlashJob, error) {
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

func (service *Service) completeDeviceFlash(ctx context.Context, job *FlashJob) error {
	transport, ok := service.flashTransport.(FlashCompletionTransport)
	if !ok {
		return nil
	}
	return transport.CompleteFlash(ctx, job.DeviceID, job)
}

func (service *Service) publishFlashEvent(ctx context.Context, job *FlashJob, phase string) {
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

	_, _ = service.events.Publish(ctx, &events.Event{
		Name:      "spark/flash/" + phase,
		Data:      string(payload),
		DeviceID:  job.DeviceID,
		ProductID: job.ProductID,
	})
}

func progressFor(job *FlashJob) int {
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

func readChunkData(ctx context.Context, path string, chunk OTAChunk) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chunk.Size <= 0 || chunk.Offset < 0 {
		return nil, ErrNotFound
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
