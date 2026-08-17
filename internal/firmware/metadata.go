package firmware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// UploadProductFirmware writes the binary file and stores its searchable metadata.
func (service *Service) UploadProductFirmware(
	ctx context.Context,
	upload Upload,
) (*ProductFirmware, error) {
	if upload.ProductID == "" || upload.Reader == nil {
		return nil, ErrNotFound
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

	firmware := &ProductFirmware{
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

func (service *Service) ListProductFirmware(
	ctx context.Context,
	productID string,
) ([]ProductFirmware, error) {
	if productID == "" {
		return nil, ErrNotFound
	}
	if service.firmwares == nil {
		return []ProductFirmware{}, nil
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
	ctx context.Context,
	productID string,
	firmwareID string,
) (*ProductFirmware, error) {
	if productID == "" || firmwareID == "" || service.firmwares == nil {
		return nil, ErrNotFound
	}

	firmware, err := service.firmwares.GetByID(ctx, firmwareID)
	if err == nil {
		if firmware.ProductID != productID {
			return nil, ErrNotFound
		}
		return firmware, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	version, ok := intFromString(firmwareID)
	if !ok {
		return nil, ErrNotFound
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
	return nil, ErrNotFound
}

func (service *Service) ReleaseProductFirmware(
	ctx context.Context,
	productID string,
	firmwareID string,
) (*ProductFirmware, error) {
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
	ctx context.Context,
	productID string,
	firmwareID string,
) (*ProductFirmware, error) {
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
	ctx context.Context,
	productID string,
	firmwareID string,
	update Update,
) (*ProductFirmware, error) {
	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return nil, err
	}

	if update.Version != nil {
		if *update.Version <= 0 {
			return nil, ErrNotFound
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
	ctx context.Context,
	productID string,
	firmwareID string,
) error {
	if service.firmwares == nil {
		return ErrNotFound
	}

	firmware, err := service.GetProductFirmware(ctx, productID, firmwareID)
	if err != nil {
		return err
	}
	if service.hasActiveFlashJobForFirmware(ctx, firmware.ID) {
		return ErrConflict
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
	ctx context.Context,
	id string,
	filename string,
	reader io.Reader,
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

func newFirmwareID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
