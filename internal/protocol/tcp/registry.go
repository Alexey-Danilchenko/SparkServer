// Package tcp contains the live device connection registry.
package tcp

import (
	"net"
	"sync"
	"time"
)

// Connection is operator-visible metadata for one connected device.
type Connection struct {
	DeviceID    string
	RemoteAddr  string
	ConnectedAt time.Time
	LastSeenAt  time.Time
}

// Registry tracks live TCP connections and command-capable clients by device ID.
type Registry struct {
	mutex       sync.RWMutex
	connections map[string]Connection
	clients     map[string]*Client
	clock       func() time.Time
}

// NewRegistry creates an empty in-memory connection registry.
func NewRegistry() *Registry {
	return &Registry{
		connections: make(map[string]Connection),
		clients:     make(map[string]*Client),
		clock:       time.Now,
	}
}

func (registry *Registry) Register(deviceID string, remoteAddr net.Addr) Connection {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	now := registry.clock().UTC()
	connection := Connection{
		DeviceID:    deviceID,
		RemoteAddr:  remoteAddr.String(),
		ConnectedAt: now,
		LastSeenAt:  now,
	}
	registry.connections[deviceID] = connection
	return connection
}

// RegisterClient records both presence and the command bridge for a device.
func (registry *Registry) RegisterClient(
	deviceID   string,
	remoteAddr net.Addr,
	client     *Client,
) Connection {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	now := registry.clock().UTC()
	connection := Connection{
		DeviceID:    deviceID,
		RemoteAddr:  remoteAddr.String(),
		ConnectedAt: now,
		LastSeenAt:  now,
	}
	registry.connections[deviceID] = connection
	registry.clients[deviceID] = client
	return connection
}

func (registry *Registry) Touch(deviceID string) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	connection, ok := registry.connections[deviceID]
	if !ok {
		return false
	}

	connection.LastSeenAt = registry.clock().UTC()
	registry.connections[deviceID] = connection
	return true
}

func (registry *Registry) Unregister(deviceID string) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	delete(registry.connections, deviceID)
	if client, ok := registry.clients[deviceID]; ok {
		client.CloseWithError()
	}
	delete(registry.clients, deviceID)
}

func (registry *Registry) Get(deviceID string) (Connection, bool) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	connection, ok := registry.connections[deviceID]
	return connection, ok
}

func (registry *Registry) GetClient(deviceID string) (*Client, bool) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	client, ok := registry.clients[deviceID]
	return client, ok
}

func (registry *Registry) List() []Connection {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	connections := make([]Connection, 0, len(registry.connections))
	for _, connection := range registry.connections {
		connections = append(connections, connection)
	}

	return connections
}

func (registry *Registry) Count() int {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	return len(registry.connections)
}
