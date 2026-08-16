// Package products implements Particle-style product/fleet metadata and membership.
package products

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
)

// CreateRequest is the service-level payload for creating a product/fleet.
type CreateRequest struct {
	OwnerID     string
	ID          string
	Slug        string
	Name        string
	Description string
}

type UpdateRequest struct {
	Name        string
	Slug        string
	Description string
}

// ProductDeviceUpdateRequest mirrors mutable product-device policy fields.
type ProductDeviceUpdateRequest struct {
	Notes                  *string
	Denied                 *bool
	Development            *bool
	Quarantined            *bool
	DesiredFirmwareVersion *int
	ClearDesiredFirmware   bool
}

// Service coordinates products with device membership records.
type Service struct {
	products       repository.ProductRepository
	productDevices repository.ProductDeviceRepository
	firmwares      repository.ProductFirmwareRepository
	devices        repository.DeviceRepository
	clock          func() time.Time
}

// NewService binds product operations to product and device repositories.
func NewService(
	products       repository.ProductRepository,
	productDevices repository.ProductDeviceRepository,
	devices        repository.DeviceRepository,
) *Service {
	return &Service{
		products:       products,
		productDevices: productDevices,
		devices:        devices,
		clock:          time.Now,
	}
}

// SetProductFirmwareRepository lets product-device updates validate desired versions.
func (service *Service) SetProductFirmwareRepository(
	firmwares repository.ProductFirmwareRepository,
) {
	service.firmwares = firmwares
}

// Create validates owner/slug uniqueness and stores a new product.
func (service *Service) Create(
	ctx     context.Context,
	request CreateRequest,
) (*domain.Product, error) {
	if request.OwnerID == "" {
		return nil, repository.ErrNotFound
	}

	slug := cleanSlug(request.Slug)
	if slug == "" {
		slug = cleanSlug(request.Name)
	}
	if slug == "" {
		return nil, repository.ErrNotFound
	}

	id := request.ID
	if id == "" {
		id = newProductID()
	}

	if _, err := service.products.GetByID(ctx, id); err == nil {
		return nil, repository.ErrConflict
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if _, err := service.products.GetBySlug(ctx, slug); err == nil {
		return nil, repository.ErrConflict
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	now := service.clock().UTC()
	product := &domain.Product{
		ID:          id,
		Slug:        slug,
		Name:        firstNonEmpty(request.Name, slug),
		Description: request.Description,
		OwnerID:     request.OwnerID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := service.products.Create(ctx, product); err != nil {
		return nil, err
	}
	return product, nil
}

func (service *Service) List(ctx context.Context, ownerID string) ([]domain.Product, error) {
	products, err := service.products.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.Product, 0)
	for _, product := range products {
		if product.OwnerID == ownerID {
			matches = append(matches, product)
		}
	}

	sort.Slice(matches, func(left int, right int) bool {
		return matches[left].CreatedAt.Before(matches[right].CreatedAt)
	})
	return matches, nil
}

func (service *Service) Get(
	ctx      context.Context,
	ownerID  string,
	idOrSlug string,
) (*domain.Product, error) {
	product, err := service.productByIDOrSlug(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}
	if product.OwnerID != ownerID {
		return nil, repository.ErrNotFound
	}
	return product, nil
}

// Config returns the minimal product config shape expected by clients.
func (service *Service) Config(
	ctx      context.Context,
	ownerID  string,
	idOrSlug string,
) (map[string]any, error) {
	product, err := service.Get(ctx, ownerID, idOrSlug)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":         product.ID,
		"product_id": product.ID,
		"slug":       product.Slug,
		"name":       product.Name,
		"owner_id":   product.OwnerID,
	}, nil
}

func (service *Service) Update(
	ctx      context.Context,
	ownerID  string,
	idOrSlug string,
	request  UpdateRequest,
) (*domain.Product, error) {
	product, err := service.Get(ctx, ownerID, idOrSlug)
	if err != nil {
		return nil, err
	}

	if request.Slug != "" {
		slug := cleanSlug(request.Slug)
		existing, err := service.products.GetBySlug(ctx, slug)
		if err == nil && existing.ID != product.ID {
			return nil, repository.ErrConflict
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		product.Slug = slug
	}
	if request.Name != "" {
		product.Name = request.Name
	}
	if request.Description != "" {
		product.Description = request.Description
	}
	product.UpdatedAt = service.clock().UTC()
	return product, service.products.Save(ctx, product)
}

func (service *Service) Delete(ctx context.Context, ownerID string, idOrSlug string) error {
	product, err := service.Get(ctx, ownerID, idOrSlug)
	if err != nil {
		return err
	}

	devices, err := service.productDevices.GetByProductID(ctx, product.ID)
	if err != nil {
		return err
	}
	for _, productDevice := range devices {
		_ = service.productDevices.Delete(ctx, productDevice.ID)
	}
	return service.products.Delete(ctx, product.ID)
}

func (service *Service) AddDevice(
	ctx             context.Context,
	ownerID         string,
	productIDOrSlug string,
	deviceID        string,
) (*domain.ProductDevice, error) {
	if deviceID == "" {
		return nil, repository.ErrNotFound
	}

	product, err := service.Get(ctx, ownerID, productIDOrSlug)
	if err != nil {
		return nil, err
	}
	device, err := service.devices.GetByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if device.OwnerID != ownerID {
		return nil, repository.ErrNotFound
	}

	links, err := service.productDevices.GetByProductID(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	for index := range links {
		if links[index].DeviceID == deviceID {
			return &links[index], nil
		}
	}

	now := service.clock().UTC()
	link := &domain.ProductDevice{
		ID:        newProductID(),
		ProductID: product.ID,
		DeviceID:  deviceID,
		OwnerID:   ownerID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := service.productDevices.Create(ctx, link); err != nil {
		return nil, err
	}

	device.ProductID = product.ID
	device.UpdatedAt = now
	if err := service.devices.Save(ctx, device); err != nil {
		return nil, err
	}

	return link, nil
}

func (service *Service) ListDevices(
	ctx             context.Context,
	ownerID         string,
	productIDOrSlug string,
) ([]domain.Device, error) {
	product, err := service.Get(ctx, ownerID, productIDOrSlug)
	if err != nil {
		return nil, err
	}
	links, err := service.productDevices.GetByProductID(ctx, product.ID)
	if err != nil {
		return nil, err
	}

	devices := make([]domain.Device, 0, len(links))
	for _, link := range links {
		device, err := service.devices.GetByID(ctx, link.DeviceID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if device.OwnerID == ownerID {
			devices = append(devices, *device)
		}
	}

	sort.Slice(devices, func(left int, right int) bool {
		return devices[left].CreatedAt.Before(devices[right].CreatedAt)
	})
	return devices, nil
}

func (service *Service) GetDevice(
	ctx             context.Context,
	ownerID         string,
	productIDOrSlug string,
	deviceID        string,
) (*domain.Device, *domain.ProductDevice, error) {
	product, err := service.Get(ctx, ownerID, productIDOrSlug)
	if err != nil {
		return nil, nil, err
	}

	links, err := service.productDevices.GetByProductID(ctx, product.ID)
	if err != nil {
		return nil, nil, err
	}
	for index := range links {
		if links[index].DeviceID != deviceID {
			continue
		}
		device, err := service.devices.GetByID(ctx, deviceID)
		if err != nil {
			return nil, nil, err
		}
		if device.OwnerID != ownerID {
			return nil, nil, repository.ErrNotFound
		}
		return device, &links[index], nil
	}
	return nil, nil, repository.ErrNotFound
}

func (service *Service) UpdateDevice(
	ctx             context.Context,
	ownerID         string,
	productIDOrSlug string,
	deviceID        string,
	request         ProductDeviceUpdateRequest,
) (*domain.ProductDevice, error) {
	_, link, err := service.GetDevice(ctx, ownerID, productIDOrSlug, deviceID)
	if err != nil {
		return nil, err
	}

	if request.Notes != nil {
		link.Notes = *request.Notes
	}
	if request.Denied != nil {
		link.Denied = *request.Denied
	}
	if request.Development != nil {
		link.Development = *request.Development
	}
	if request.Quarantined != nil {
		link.Quarantined = *request.Quarantined
	}
	if request.ClearDesiredFirmware {
		link.DesiredFirmwareVersion = nil
	} else if request.DesiredFirmwareVersion != nil {
		version := *request.DesiredFirmwareVersion
		if err := service.validateFirmwareVersion(ctx, link.ProductID, version); err != nil {
			return nil, err
		}
		link.DesiredFirmwareVersion = &version
	}
	link.UpdatedAt = service.clock().UTC()
	if err := service.productDevices.Save(ctx, link); err != nil {
		return nil, err
	}
	return link, nil
}

func (service *Service) RemoveDevice(
	ctx             context.Context,
	ownerID         string,
	productIDOrSlug string,
	deviceID        string,
) error {
	product, err := service.Get(ctx, ownerID, productIDOrSlug)
	if err != nil {
		return err
	}
	links, err := service.productDevices.GetByProductID(ctx, product.ID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.DeviceID != deviceID {
			continue
		}
		if err := service.productDevices.Delete(ctx, link.ID); err != nil {
			return err
		}
		if device, err := service.devices.GetByID(ctx, deviceID); err == nil && device.ProductID == product.ID {
			device.ProductID = ""
			device.UpdatedAt = service.clock().UTC()
			return service.devices.Save(ctx, device)
		}
		return nil
	}
	return repository.ErrNotFound
}

func (service *Service) validateFirmwareVersion(
	ctx       context.Context,
	productID string,
	version   int,
) error {
	if service.firmwares == nil {
		return nil
	}

	firmwares, err := service.firmwares.GetByProductID(ctx, productID)
	if err != nil {
		return err
	}
	for index := range firmwares {
		if firmwares[index].Version == version {
			return nil
		}
	}
	return repository.ErrNotFound
}

func (service *Service) productByIDOrSlug(
	ctx      context.Context,
	idOrSlug string,
) (*domain.Product, error) {
	if idOrSlug == "" {
		return nil, repository.ErrNotFound
	}
	if product, err := service.products.GetByID(ctx, idOrSlug); err == nil {
		return product, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	return service.products.GetBySlug(ctx, idOrSlug)
}

func cleanSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.Trim(value, "-")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func newProductID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
