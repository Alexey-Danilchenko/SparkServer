// Package devices implements claim/provisioning, metadata, and live device commands.
package devices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"sparkserver/internal/domain"
	"sparkserver/internal/repository"
)

// ErrDeviceOffline is returned when an operation requires a connected TCP session.
var ErrDeviceOffline = domain.ErrDeviceOffline

// ErrDeviceTimeout is returned when a live TCP command exceeds API_TIMEOUT.
var ErrDeviceTimeout = domain.ErrDeviceTimeout

// LiveDeviceClient is implemented by the TCP server to bridge REST calls to devices.
type LiveDeviceClient interface {
	GetVariable(ctx context.Context, deviceID string, variableName string) (string, error)
	CallFunction(ctx context.Context, deviceID string, functionName string, argument string) (int, error)
	Ping(ctx context.Context, deviceID string) error
}

// Service owns device claims, ownership, live metadata, and REST-to-TCP commands.
type Service struct {
	devices       repository.DeviceRepository
	deviceClaims  repository.DeviceClaimRepository
	liveClient    LiveDeviceClient
	apiTimeout    time.Duration
	claimLifetime time.Duration
	clock         func() time.Time
}

// NewService builds device behavior over repository abstractions.
func NewService(
	devices      repository.DeviceRepository,
	deviceClaims repository.DeviceClaimRepository,
) *Service {
	return &Service{
		devices:       devices,
		deviceClaims:  deviceClaims,
		apiTimeout:    30 * time.Second,
		claimLifetime: 10 * time.Minute,
		clock:         time.Now,
	}
}

// SetLiveClient attaches the TCP command bridge after both services are constructed.
func (service *Service) SetLiveClient(liveClient LiveDeviceClient) {
	service.liveClient = liveClient
}

func (service *Service) SetAPITimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	service.apiTimeout = timeout
}

// Claim assigns a device to an owner, creating a placeholder record if needed.
func (service *Service) Claim(
	ctx      context.Context,
	ownerID  string,
	deviceID string,
) (*domain.Device, error) {
	if deviceID == "" {
		return nil, repository.ErrNotFound
	}

	now := service.clock().UTC()
	device, err := service.devices.GetByID(ctx, deviceID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}

		device = &domain.Device{
			ID:        deviceID,
			OwnerID:   ownerID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return device, service.devices.Create(ctx, device)
	}

	device.OwnerID = ownerID
	device.UpdatedAt = now
	return device, service.devices.Save(ctx, device)
}

func (service *Service) CreateClaimCode(
	ctx     context.Context,
	ownerID string,
) (*domain.DeviceClaim, error) {
	now := service.clock().UTC()
	claim := domain.DeviceClaim{
		Code:      newClaimCode(),
		OwnerID:   ownerID,
		ExpiresAt: now.Add(service.claimLifetime),
		CreatedAt: now,
	}

	if service.deviceClaims == nil {
		return &claim, nil
	}

	return &claim, service.deviceClaims.Create(ctx, &claim)
}

// Provision consumes a claim code and claims the connecting device.
func (service *Service) Provision(
	ctx       context.Context,
	deviceID  string,
	claimCode string,
) (*domain.Device, error) {
	if service.deviceClaims == nil {
		return nil, repository.ErrNotFound
	}

	claim, err := service.deviceClaims.GetByID(ctx, claimCode)
	if err != nil {
		return nil, err
	}

	now := service.clock().UTC()
	if claim.UsedAt != nil || now.After(claim.ExpiresAt) {
		return nil, repository.ErrNotFound
	}

	device, err := service.Claim(ctx, claim.OwnerID, deviceID)
	if err != nil {
		return nil, err
	}

	claim.UsedAt = &now
	claim.DeviceID = deviceID
	if err := service.deviceClaims.Save(ctx, claim); err != nil {
		return nil, err
	}

	return device, nil
}

func (service *Service) List(ctx context.Context, ownerID string) ([]domain.Device, error) {
	devices, err := service.devices.List(ctx)
	if err != nil {
		return nil, err
	}

	matches := make([]domain.Device, 0)
	for _, device := range devices {
		if device.OwnerID == ownerID {
			matches = append(matches, device)
		}
	}

	return matches, nil
}

// MarkConnected records TCP presence as devices complete the protocol handshake.
func (service *Service) MarkConnected(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return repository.ErrNotFound
	}

	now := service.clock().UTC()
	device, err := service.devices.GetByID(ctx, deviceID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}

		device = &domain.Device{
			ID:          deviceID,
			Connected:   true,
			LastHeardAt: &now,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return service.devices.Create(ctx, device)
	}

	device.Connected = true
	device.LastHeardAt = &now
	device.UpdatedAt = now
	return service.devices.Save(ctx, device)
}

// UpdateDescription persists variables, functions, and attributes advertised by firmware.
func (service *Service) UpdateDescription(
	ctx         context.Context,
	deviceID    string,
	description domain.DeviceDescription,
) (*domain.Device, error) {
	if deviceID == "" {
		return nil, repository.ErrNotFound
	}

	now := service.clock().UTC()
	device, err := service.devices.GetByID(ctx, deviceID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}

		device = &domain.Device{
			ID:        deviceID,
			CreatedAt: now,
		}
	}

	device.Connected = true
	device.LastHeardAt = &now
	device.UpdatedAt = now
	device.Variables = copyStringMap(description.Variables)
	device.Functions = copyStringSlice(description.Functions)
	device.Attributes = copyStringMap(description.Attributes)
	if productID := firstNonEmptyString(
		device.Attributes["product_id"],
		device.Attributes["productID"],
		device.Attributes["product"],
	); productID != "" {
		device.ProductID = productID
	}

	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}

	if err != nil && errors.Is(err, repository.ErrNotFound) {
		return device, service.devices.Create(ctx, device)
	}
	return device, service.devices.Save(ctx, device)
}

func (service *Service) MarkDisconnected(ctx context.Context, deviceID string) error {
	device, err := service.devices.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}

	device.Connected = false
	device.UpdatedAt = service.clock().UTC()
	return service.devices.Save(ctx, device)
}

func (service *Service) Get(
	ctx      context.Context,
	ownerID  string,
	idOrName string,
) (*domain.Device, error) {
	if device, err := service.devices.GetByID(ctx, idOrName); err == nil {
		if device.OwnerID != ownerID {
			return nil, repository.ErrNotFound
		}
		return device, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	return service.devices.GetByName(ctx, ownerID, idOrName)
}

func (service *Service) Update(
	ctx      context.Context,
	ownerID  string,
	idOrName string,
	name     string,
) (*domain.Device, error) {
	device, err := service.Get(ctx, ownerID, idOrName)
	if err != nil {
		return nil, err
	}

	device.Name = name
	device.UpdatedAt = service.clock().UTC()
	return device, service.devices.Save(ctx, device)
}

func (service *Service) GetVariable(
	ctx          context.Context,
	ownerID      string,
	idOrName     string,
	variableName string,
) (string, error) {
	if variableName == "" {
		return "", repository.ErrNotFound
	}

	device, err := service.Get(ctx, ownerID, idOrName)
	if err != nil {
		return "", err
	}
	if service.liveClient == nil || !device.Connected {
		if value, ok := device.Variables[variableName]; ok {
			return value, nil
		}
		return "", ErrDeviceOffline
	}

	callContext, cancel := service.liveCallContext(ctx)
	defer cancel()

	value, err := service.liveClient.GetVariable(callContext, device.ID, variableName)
	return value, mapLiveCallError(err)
}

func (service *Service) CallFunction(
	ctx          context.Context,
	ownerID      string,
	idOrName     string,
	functionName string,
	argument     string,
) (int, error) {
	if functionName == "" {
		return 0, repository.ErrNotFound
	}

	device, err := service.Get(ctx, ownerID, idOrName)
	if err != nil {
		return 0, err
	}
	if service.liveClient == nil || !device.Connected {
		return 0, ErrDeviceOffline
	}

	callContext, cancel := service.liveCallContext(ctx)
	defer cancel()

	returnValue, err := service.liveClient.CallFunction(callContext, device.ID, functionName, argument)
	return returnValue, mapLiveCallError(err)
}

func (service *Service) Ping(
	ctx      context.Context,
	ownerID  string,
	idOrName string,
) (*domain.Device, error) {
	device, err := service.Get(ctx, ownerID, idOrName)
	if err != nil {
		return nil, err
	}
	if service.liveClient == nil || !device.Connected {
		return device, ErrDeviceOffline
	}

	callContext, cancel := service.liveCallContext(ctx)
	defer cancel()

	return device, mapLiveCallError(service.liveClient.Ping(callContext, device.ID))
}

func (service *Service) Unclaim(ctx context.Context, ownerID string, idOrName string) error {
	device, err := service.Get(ctx, ownerID, idOrName)
	if err != nil {
		return err
	}

	device.OwnerID = ""
	device.UpdatedAt = service.clock().UTC()
	return service.devices.Save(ctx, device)
}

func newClaimCode() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}

	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyStringSlice(values []string) []string {
	if values == nil {
		return nil
	}

	return append([]string(nil), values...)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (service *Service) liveCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if service.apiTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, service.apiTimeout)
}

func mapLiveCallError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrDeviceTimeout
	}
	return err
}
