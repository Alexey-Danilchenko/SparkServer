// Package domain contains persistent records used by file repositories and APIs.
package domain

import "time"

// User is a local Spark/Particle account.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Scopes       []string  `json:"scopes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (user User) GetID() string {
	return user.ID
}

// AccessToken is a bearer credential returned by the OAuth password flow.
type AccessToken struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (token AccessToken) GetID() string {
	return token.Token
}

// Device represents a claimed Particle-compatible device and its live metadata.
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

func (device Device) GetID() string {
	return device.ID
}

// DeviceKey stores a device public key registered during provisioning.
type DeviceKey struct {
	DeviceID  string    `json:"device_id"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (key DeviceKey) GetID() string {
	return key.DeviceID
}

// DeviceClaim is a short-lived claim code used by device provisioning flows.
type DeviceClaim struct {
	Code      string     `json:"code"`
	OwnerID   string     `json:"owner_id"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	DeviceID  string     `json:"device_id,omitempty"`
}

func (claim DeviceClaim) GetID() string {
	return claim.Code
}

// Product groups devices and firmware into a Particle-style fleet.
type Product struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	OwnerID     string    `json:"owner_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (product Product) GetID() string {
	return product.ID
}

// ProductDevice stores the relationship and policy metadata for a device in a product.
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

func (device ProductDevice) GetID() string {
	return device.ID
}

// ProductFirmware describes an uploaded prebuilt binary and release/default flags.
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

func (firmware ProductFirmware) GetID() string {
	return firmware.ID
}

// FlashJob tracks OTA delivery of one firmware binary to one device.
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

func (job FlashJob) GetID() string {
	return job.ID
}

// OTAChunk describes one padded transfer chunk in a device flash job.
type OTAChunk struct {
	Index       int    `json:"index"`
	Offset      int64  `json:"offset"`
	Size        int    `json:"size"`
	SHA256      string `json:"sha256"`
	Transferred bool   `json:"transferred"`
}

// Webhook persists an event subscription and its latest delivery state.
type Webhook struct {
	ID              string            `json:"id"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Event           string            `json:"event"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	FailCount       int               `json:"fail_count,omitempty"`
	LastStatus      int               `json:"last_status,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time        `json:"last_delivered_at,omitempty"`
	NextAttemptAt   *time.Time        `json:"next_attempt_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func (webhook Webhook) GetID() string {
	return webhook.ID
}

// Event is a published device, product, or server event.
type Event struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Data      string    `json:"data,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	ProductID string    `json:"product_id,omitempty"`
	Published time.Time `json:"published"`
}

func (event Event) GetID() string {
	return event.ID
}
