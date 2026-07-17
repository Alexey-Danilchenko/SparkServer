// Package domain defines storage and API data models shared across services.
package domain

// DeviceDescription stores variables, functions, and attributes advertised by firmware.
type DeviceDescription struct {
	Variables  map[string]string `json:"variables,omitempty"`
	Functions  []string          `json:"functions,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}
