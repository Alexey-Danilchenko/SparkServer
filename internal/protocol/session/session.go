// Package session defines the authenticated state for one connected device.
package session

// Session carries the device ID and symmetric key established during handshake.
type Session struct {
	DeviceID   string
	SessionKey []byte
}
