// Package test verifies firmware service storage and flash-job state.
package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	filerepo "sparkserver/internal/repository/file"
)

func TestFirmwareFlashJobManifestAndTransitions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashChunkSize(512)

	payload := bytes.Repeat([]byte{0xab}, 1200)
	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	if job.Status != firmware.FlashStatusQueued || job.Progress != 0 {
		t.Fatalf("initial job state = %#v", job)
	}
	if job.ChunkSize != 512 || job.ChunkCount != 3 || len(job.Chunks) != 3 {
		t.Fatalf("manifest = %#v", job)
	}
	if job.Chunks[0].Offset != 0 || job.Chunks[0].Size != 512 {
		t.Fatalf("chunk 0 = %#v", job.Chunks[0])
	}
	if job.Chunks[2].Offset != 1024 || job.Chunks[2].Size != 176 {
		t.Fatalf("chunk 2 = %#v", job.Chunks[2])
	}
	expectedChunkHash := sha256.Sum256(payload[:512])
	if job.Chunks[0].SHA256 != hex.EncodeToString(expectedChunkHash[:]) {
		t.Fatalf("chunk hash = %s", job.Chunks[0].SHA256)
	}

	running, err := service.StartFlashJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if running.Status != firmware.FlashStatusRunning || running.StartedAt == nil {
		t.Fatalf("running job = %#v", running)
	}

	afterFirstChunk, err := service.MarkFlashChunkTransferred(ctx, job.ID, 0)
	if err != nil {
		t.Fatalf("mark chunk 0: %v", err)
	}
	if afterFirstChunk.Transferred != 1 || afterFirstChunk.Progress != 33 || !afterFirstChunk.Chunks[0].Transferred {
		t.Fatalf("after chunk 0 = %#v", afterFirstChunk)
	}

	afterDuplicate, err := service.MarkFlashChunkTransferred(ctx, job.ID, 0)
	if err != nil {
		t.Fatalf("mark duplicate chunk 0: %v", err)
	}
	if afterDuplicate.Transferred != 1 || afterDuplicate.Progress != 33 {
		t.Fatalf("after duplicate = %#v", afterDuplicate)
	}

	if _, err := service.MarkFlashChunkTransferred(ctx, job.ID, 1); err != nil {
		t.Fatalf("mark chunk 1: %v", err)
	}
	completed, err := service.MarkFlashChunkTransferred(ctx, job.ID, 2)
	if err != nil {
		t.Fatalf("mark chunk 2: %v", err)
	}
	if completed.Status != firmware.FlashStatusCompleted || completed.Progress != 100 || completed.CompletedAt == nil {
		t.Fatalf("completed job = %#v", completed)
	}
}

func TestFirmwareFlashJobFailureTransition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	failed, err := service.FailFlashJob(ctx, job.ID, "device disconnected")
	if err != nil {
		t.Fatalf("fail flash: %v", err)
	}
	if failed.Status != firmware.FlashStatusFailed || failed.Error != "device disconnected" || failed.CompletedAt == nil {
		t.Fatalf("failed job = %#v", failed)
	}
}

func TestFirmwareReleaseDefaultAndUpdateCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)

	first, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Version:   1,
		Filename:  "v1.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01}),
	})
	if err != nil {
		t.Fatalf("upload first: %v", err)
	}
	second, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Version:   2,
		Filename:  "v2.bin",
		Reader:    bytes.NewReader([]byte{0x02}),
	})
	if err != nil {
		t.Fatalf("upload second: %v", err)
	}

	released, err := service.ReleaseProductFirmware(ctx, "product-1", second.ID)
	if err != nil {
		t.Fatalf("release second: %v", err)
	}
	if !released.Released || released.Default || released.Current {
		t.Fatalf("released = %#v", released)
	}

	defaultFirmware, err := service.SetDefaultProductFirmware(ctx, "product-1", second.ID)
	if err != nil {
		t.Fatalf("set default: %v", err)
	}
	if !defaultFirmware.Released || !defaultFirmware.Default || !defaultFirmware.Current {
		t.Fatalf("default = %#v", defaultFirmware)
	}

	firstAfterDefault, err := service.GetProductFirmware(ctx, "product-1", first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstAfterDefault.Current || firstAfterDefault.Default {
		t.Fatalf("first after default = %#v", firstAfterDefault)
	}

	target, updateAvailable, err := service.CheckProductFirmwareUpdate(ctx, firmware.UpdateCheckRequest{
		ProductID:      "product-1",
		CurrentVersion: 1,
	})
	if err != nil {
		t.Fatalf("check update: %v", err)
	}
	if !updateAvailable || target.ID != second.ID {
		t.Fatalf("update target = %#v available=%v", target, updateAvailable)
	}

	_, updateAvailable, err = service.CheckProductFirmwareUpdate(ctx, firmware.UpdateCheckRequest{
		ProductID:      "product-1",
		CurrentVersion: 2,
	})
	if err != nil {
		t.Fatalf("check no update: %v", err)
	}
	if updateAvailable {
		t.Fatal("expected no update for current version")
	}
}

func TestFirmwareAutoUpdateQueuesDefaultFirmware(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Version:   2,
		Filename:  "v2.bin",
		Default:   true,
		Reader:    bytes.NewReader([]byte{0x02, 0x03}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, started, err := service.CheckAndStartProductFirmwareUpdate(ctx, &domain.Device{
		ID:        "device-1",
		ProductID: "product-1",
		Attributes: map[string]string{
			"product_firmware_version": "1",
		},
	})
	if err != nil {
		t.Fatalf("auto update: %v", err)
	}
	if !started || job == nil || job.FirmwareID != uploaded.ID || job.Status != firmware.FlashStatusQueued {
		t.Fatalf("job = %#v started=%v", job, started)
	}

	duplicate, started, err := service.CheckAndStartProductFirmwareUpdate(ctx, &domain.Device{
		ID:        "device-1",
		ProductID: "product-1",
		Attributes: map[string]string{
			"product_firmware_version": "1",
		},
	})
	if err != nil {
		t.Fatalf("duplicate auto update: %v", err)
	}
	if started || duplicate != nil {
		t.Fatalf("duplicate job = %#v started=%v", duplicate, started)
	}
}

func TestFirmwareAutoUpdateUsesDesiredProductDeviceVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	productDevices := filerepo.NewProductDeviceRepository(dir + "/productDevices")
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetProductDeviceResolver(productDevices)

	defaultFirmware, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Version:   3,
		Filename:  "v3.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x03}),
	})
	if err != nil {
		t.Fatalf("upload default firmware: %v", err)
	}
	desiredFirmware, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Version:   2,
		Filename:  "v2.bin",
		Reader:    bytes.NewReader([]byte{0x02}),
	})
	if err != nil {
		t.Fatalf("upload desired firmware: %v", err)
	}
	desiredVersion := 2
	if err := productDevices.Create(ctx, &domain.ProductDevice{
		ID:                     "link-1",
		ProductID:              "product-1",
		DeviceID:               "device-1",
		DesiredFirmwareVersion: &desiredVersion,
	}); err != nil {
		t.Fatalf("create product device: %v", err)
	}

	job, started, err := service.CheckAndStartProductFirmwareUpdate(ctx, &domain.Device{
		ID:        "device-1",
		ProductID: "product-1",
		Attributes: map[string]string{
			"product_firmware_version": "1",
		},
	})
	if err != nil {
		t.Fatalf("desired auto update: %v", err)
	}
	if !started || job == nil || job.FirmwareID != desiredFirmware.ID || job.FirmwareID == defaultFirmware.ID {
		t.Fatalf("job = %#v started=%v desired=%s default=%s", job, started, desiredFirmware.ID, defaultFirmware.ID)
	}
}

func TestFirmwareFlashJobTransportNegotiationStartsJob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashTransport(transport)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	if job.Status != firmware.FlashStatusCompleted || job.StartedAt == nil || job.CompletedAt == nil {
		t.Fatalf("job = %#v", job)
	}
	if transport.deviceID != "device-1" || transport.job == nil || transport.job.ID != job.ID {
		t.Fatalf("transport = %#v", transport)
	}
	if len(transport.chunks) != 1 || transport.chunks[0].Index != 0 || !bytes.Equal(transport.payloads[0], []byte{0x01, 0x02}) {
		t.Fatalf("chunks = %#v payloads=%x", transport.chunks, transport.payloads)
	}
}

func TestFirmwareStartDeviceFlashPumpsAllChunks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashChunkSize(2)
	service.SetFlashTransport(transport)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02, 0x03}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	if job.Status != firmware.FlashStatusCompleted || job.Transferred != 2 || job.Progress != 100 {
		t.Fatalf("job = %#v", job)
	}
	if len(transport.chunks) != 2 || transport.chunks[0].Index != 0 || transport.chunks[1].Index != 1 {
		t.Fatalf("chunks = %#v", transport.chunks)
	}
	if !bytes.Equal(transport.payloads[0], []byte{0x01, 0x02}) || !bytes.Equal(transport.payloads[1], []byte{0x03}) {
		t.Fatalf("payloads = %x", transport.payloads)
	}
}

func TestFirmwareFlashJobTransportFailurePersistsFailedJob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{err: errors.New("device rejected flash")}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashTransport(transport)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if job.Status != firmware.FlashStatusFailed || job.Error != "device rejected flash" {
		t.Fatalf("failed job = %#v", job)
	}

	jobs, err := service.ListDeviceFlashJobs(ctx, "device-1")
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != firmware.FlashStatusFailed {
		t.Fatalf("jobs = %#v", jobs)
	}
}

func TestFirmwareSendNextFlashChunkMarksProgressOnAck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashChunkSize(2)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02, 0x03}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	service.SetFlashTransport(transport)
	afterFirst, err := service.SendNextFlashChunk(ctx, job.ID)
	if err != nil {
		t.Fatalf("send first chunk: %v", err)
	}
	if afterFirst.Transferred != 1 || afterFirst.Progress != 50 || afterFirst.Status != firmware.FlashStatusRunning {
		t.Fatalf("after first chunk = %#v", afterFirst)
	}
	if transport.chunk.Index != 0 || !bytes.Equal(transport.data, []byte{0x01, 0x02}) {
		t.Fatalf("transport chunk = %#v data=%x", transport.chunk, transport.data)
	}

	afterSecond, err := service.SendNextFlashChunk(ctx, job.ID)
	if err != nil {
		t.Fatalf("send second chunk: %v", err)
	}
	if afterSecond.Status != firmware.FlashStatusCompleted || afterSecond.Progress != 100 || afterSecond.CompletedAt == nil {
		t.Fatalf("after second chunk = %#v", afterSecond)
	}
}

func TestFirmwareSendFlashChunkFailureMarksJobFailed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{chunkErr: errors.New("chunk rejected")}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}

	service.SetFlashTransport(transport)
	failed, err := service.SendNextFlashChunk(ctx, job.ID)
	if err == nil {
		t.Fatal("expected chunk send error")
	}
	if failed.Status != firmware.FlashStatusFailed || failed.Error != "chunk rejected" {
		t.Fatalf("failed job = %#v", failed)
	}
}

func TestFirmwareRetryMissedFlashChunksResendsRequestedChunks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	transport := &fakeFlashTransport{}
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetFlashChunkSize(2)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04, 0x05}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}
	if _, err := service.StartFlashJob(ctx, job.ID); err != nil {
		t.Fatalf("start job: %v", err)
	}
	if _, err := service.MarkFlashChunkTransferred(ctx, job.ID, 0); err != nil {
		t.Fatalf("mark chunk 0: %v", err)
	}
	if _, err := service.MarkFlashChunkTransferred(ctx, job.ID, 1); err != nil {
		t.Fatalf("mark chunk 1: %v", err)
	}

	service.SetFlashTransport(transport)
	retried, err := service.RetryMissedFlashChunks(ctx, "device-1", []int{1, 2, 99, 1})
	if err != nil {
		t.Fatalf("retry missed chunks: %v", err)
	}
	if retried.Status != firmware.FlashStatusCompleted || retried.Progress != 100 {
		t.Fatalf("retried job = %#v", retried)
	}
	if len(transport.chunks) != 2 || transport.chunks[0].Index != 1 || transport.chunks[1].Index != 2 {
		t.Fatalf("chunks = %#v", transport.chunks)
	}
	if !bytes.Equal(transport.payloads[0], []byte{0x03, 0x04}) || !bytes.Equal(transport.payloads[1], []byte{0x05}) {
		t.Fatalf("payloads = %x", transport.payloads)
	}
}

func TestFirmwareAbortDeviceFlashMarksActiveJobFailed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}
	if _, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	}); err != nil {
		t.Fatalf("start flash: %v", err)
	}

	failed, err := service.AbortDeviceFlash(ctx, "device-1", "aborted: not enough space")
	if err != nil {
		t.Fatalf("abort flash: %v", err)
	}
	if failed.Status != firmware.FlashStatusFailed || failed.Error != "aborted: not enough space" {
		t.Fatalf("failed job = %#v", failed)
	}
}

func TestFirmwareFlashJobPublishesProgressEvents(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	eventService := events.NewService(nil)
	service := firmware.NewService(
		filerepo.NewProductFirmwareRepository(dir+"/metadata"),
		dir+"/binaries",
		filerepo.NewFlashJobRepository(dir+"/flashJobs"),
	)
	service.SetEventPublisher(eventService)

	subscriptionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := eventService.Subscribe(subscriptionCtx, events.Filter{Prefix: "spark/flash/", DeviceID: "device-1"})

	uploaded, err := service.UploadProductFirmware(ctx, firmware.Upload{
		ProductID: "product-1",
		Filename:  "firmware.bin",
		Current:   true,
		Reader:    bytes.NewReader([]byte{0x01}),
	})
	if err != nil {
		t.Fatalf("upload firmware: %v", err)
	}

	job, err := service.StartDeviceFlash(ctx, firmware.FlashRequest{
		DeviceID:   "device-1",
		ProductID:  "product-1",
		FirmwareID: uploaded.ID,
	})
	if err != nil {
		t.Fatalf("start flash: %v", err)
	}
	if _, err := service.StartFlashJob(ctx, job.ID); err != nil {
		t.Fatalf("start flash job: %v", err)
	}
	if _, err := service.MarkFlashChunkTransferred(ctx, job.ID, 0); err != nil {
		t.Fatalf("mark chunk: %v", err)
	}

	names := []string{
		receiveFlashEvent(t, events).Name,
		receiveFlashEvent(t, events).Name,
		receiveFlashEvent(t, events).Name,
	}
	expected := []string{"spark/flash/queued", "spark/flash/running", "spark/flash/completed"}
	for index := range expected {
		if names[index] != expected[index] {
			t.Fatalf("events = %#v", names)
		}
	}
}

type fakeFlashTransport struct {
	deviceID string
	job      *domain.FlashJob
	chunk    domain.OTAChunk
	data     []byte
	chunks   []domain.OTAChunk
	payloads [][]byte
	err      error
	chunkErr error
}

func (transport *fakeFlashTransport) BeginFlash(
	_ context.Context,
	deviceID string,
	job      *domain.FlashJob,
) error {
	transport.deviceID = deviceID
	transport.job = job
	return transport.err
}

func (transport *fakeFlashTransport) SendFlashChunk(
	_ context.Context,
	deviceID string,
	job      *domain.FlashJob,
	chunk    domain.OTAChunk,
	data     []byte,
) error {
	transport.deviceID = deviceID
	transport.job = job
	transport.chunk = chunk
	transport.data = append([]byte(nil), data...)
	transport.chunks = append(transport.chunks, chunk)
	transport.payloads = append(transport.payloads, append([]byte(nil), data...))
	return transport.chunkErr
}

func receiveFlashEvent(t *testing.T, events <-chan domain.Event) domain.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for flash event")
	}
	return domain.Event{}
}
