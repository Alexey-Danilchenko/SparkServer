// Package domain exposes cross-service sentinel errors.
package domain

import "errors"

// ErrDeviceOffline reports a live-only operation against a disconnected device.
var ErrDeviceOffline = errors.New("device is offline")

// ErrDeviceTimeout reports a device request that exceeded the API timeout.
var ErrDeviceTimeout = errors.New("device request timed out")
