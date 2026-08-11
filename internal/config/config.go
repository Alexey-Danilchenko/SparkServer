// Package config loads legacy Spark Server settings and maps them to Go services.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const (
	defaultHTTPPort = 8080
	defaultTCPPort  = 5683
)

// Config mirrors the old settings.json shape while exposing typed Go accessors.
type Config struct {
	DefaultAdminUsername  string     `json:"DEFAULT_ADMIN_USERNAME"`
	DefaultAdminPassword  string     `json:"DEFAULT_ADMIN_PASSWORD"`
	DeviceDirectory       string     `json:"DEVICE_DIRECTORY"`
	DeviceClaimsDirectory string     `json:"DEVICE_CLAIMS_DIRECTORY"`
	EventsDirectory       string     `json:"EVENTS_DIRECTORY"`
	FirmwareDirectory     string     `json:"FIRMWARE_DIRECTORY"`
	ProductsDirectory     string     `json:"PRODUCTS_DIRECTORY"`
	UsersDirectory        string     `json:"USERS_DIRECTORY"`
	TokensDirectory       string     `json:"TOKENS_DIRECTORY"`
	WebhooksDirectory     string     `json:"WEBHOOKS_DIRECTORY"`
	ServerKeysDirectory   string     `json:"SERVER_KEYS_DIRECTORY"`
	LoginRoute            string     `json:"LOGIN_ROUTE"`
	AccessTokenLifetime   int64      `json:"ACCESS_TOKEN_LIFETIME"`
	APITimeout            int64      `json:"API_TIMEOUT"`
	HTTP                  HTTPConfig `json:"EXPRESS_SERVER_CONFIG"`
	TCP                   TCPConfig  `json:"TCP_DEVICE_SERVER_CONFIG"`
	DB                    DBConfig   `json:"DB_CONFIG"`
}

// HTTPConfig describes the REST/SSE API listener.
type HTTPConfig struct {
	Port int `json:"PORT"`
}

// TCPConfig describes the Particle device TCP listener.
type TCPConfig struct {
	Port int `json:"PORT"`
}

// DBConfig records the configured persistence backend; file is currently implemented.
type DBConfig struct {
	Type string `json:"DB_TYPE"`
	URL  string `json:"DB_URL"`
}

// Default returns a complete file-backed local-cloud configuration.
func Default() *Config {
	return &Config{
		DefaultAdminUsername:  "__admin__",
		DefaultAdminPassword:  "adminPassword",
		DeviceDirectory:       "data/devices",
		DeviceClaimsDirectory: "data/deviceClaims",
		EventsDirectory:       "data/events",
		FirmwareDirectory:     "data/knownApps",
		ProductsDirectory:     "data/products",
		UsersDirectory:        "data/users",
		TokensDirectory:       "data/accessTokens",
		WebhooksDirectory:     "data/webhooks",
		ServerKeysDirectory:   "data",
		LoginRoute:            "/oauth/token",
		AccessTokenLifetime:   7776000,
		APITimeout:            30000,
		HTTP: HTTPConfig{
			Port: defaultHTTPPort,
		},
		TCP: TCPConfig{
			Port: defaultTCPPort,
		},
		DB: DBConfig{
			Type: "file",
		},
	}
}

// Load reads a settings.json-compatible file and fills any omitted values.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

// EnsureDirectories creates every runtime directory used by the file-backed server.
func (c *Config) EnsureDirectories() error {
	directories := []string{
		c.DeviceDirectory,
		c.DeviceClaimsDirectory,
		c.EventsDirectory,
		c.FirmwareDirectory,
		filepath.Join(c.FirmwareDirectory, "metadata"),
		filepath.Join(c.FirmwareDirectory, "binaries"),
		filepath.Join(c.FirmwareDirectory, "flashJobs"),
		c.ProductsDirectory,
		filepath.Join(c.ProductsDirectory, "devices"),
		c.UsersDirectory,
		c.TokensDirectory,
		c.WebhooksDirectory,
		c.ServerKeysDirectory,
		filepath.Join(c.ServerKeysDirectory, "deviceKeys"),
		filepath.Join("data", "db"),
	}

	for _, directory := range directories {
		if directory == "" {
			continue
		}

		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create data directory %s: %w", directory, err)
		}
	}

	return nil
}

func (c *Config) applyDefaults() {
	if c.DefaultAdminUsername == "" {
		c.DefaultAdminUsername = "__admin__"
	}
	if c.DefaultAdminPassword == "" {
		c.DefaultAdminPassword = "adminPassword"
	}
	if c.DeviceDirectory == "" {
		c.DeviceDirectory = "data/devices"
	}
	if c.DeviceClaimsDirectory == "" {
		c.DeviceClaimsDirectory = "data/deviceClaims"
	}
	if c.EventsDirectory == "" {
		c.EventsDirectory = "data/events"
	}
	if c.FirmwareDirectory == "" {
		c.FirmwareDirectory = "data/knownApps"
	}
	if c.ProductsDirectory == "" {
		c.ProductsDirectory = "data/products"
	}
	if c.UsersDirectory == "" {
		c.UsersDirectory = "data/users"
	}
	if c.TokensDirectory == "" {
		c.TokensDirectory = "data/accessTokens"
	}
	if c.WebhooksDirectory == "" {
		c.WebhooksDirectory = "data/webhooks"
	}
	if c.ServerKeysDirectory == "" {
		c.ServerKeysDirectory = "data"
	}
	if c.LoginRoute == "" {
		c.LoginRoute = "/oauth/token"
	}
	if c.AccessTokenLifetime == 0 {
		c.AccessTokenLifetime = 7776000
	}
	if c.APITimeout == 0 {
		c.APITimeout = 30000
	}
	if c.HTTP.Port == 0 {
		c.HTTP.Port = defaultHTTPPort
	}
	if c.TCP.Port == 0 {
		c.TCP.Port = defaultTCPPort
	}
	if c.DB.Type == "" {
		c.DB.Type = "file"
	}
}

// Address returns the all-interface bind address used by net/http.
func (c HTTPConfig) Address() string {
	return net.JoinHostPort("", fmt.Sprintf("%d", c.Port))
}

// Address returns the all-interface bind address used by the device TCP server.
func (c TCPConfig) Address() string {
	return net.JoinHostPort("", fmt.Sprintf("%d", c.Port))
}
