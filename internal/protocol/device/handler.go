// Package device handles decrypted device-originated CoAP messages.
package device

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/protocol/coap"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/session"
)

// EventPublisher is implemented by events.Service for device event ingestion.
type EventPublisher interface {
	Publish(ctx context.Context, event *domain.Event) (*domain.Event, error)
}

// DeviceDescriber persists firmware-advertised variables/functions/attributes.
type DeviceDescriber interface {
	UpdateDescription(ctx context.Context, deviceID string, description domain.DeviceDescription) (*domain.Device, error)
}

// DeviceFirmwareUpdater checks product firmware after a describe acknowledgement.
type DeviceFirmwareUpdater interface {
	CheckAndStartProductFirmwareUpdate(ctx context.Context, device *domain.Device) (*domain.FlashJob, bool, error)
}

// Handler maps device CoAP paths to events, descriptions, pings, and OTA checks.
type Handler struct {
	events         EventPublisher
	devices        DeviceDescriber
	firmware       DeviceFirmwareUpdater
	pendingUpdates map[uint16]*domain.Device
	mu             sync.Mutex
}

// NewHandler creates a protocol handler with optional device description persistence.
func NewHandler(events EventPublisher, devices ...DeviceDescriber) *Handler {
	handler := &Handler{
		events:         events,
		pendingUpdates: make(map[uint16]*domain.Device),
	}
	if len(devices) > 0 {
		handler.devices = devices[0]
	}
	return handler
}

func (handler *Handler) SetFirmwareUpdater(updater DeviceFirmwareUpdater) {
	handler.firmware = updater
}

// Handle processes one decrypted device packet and returns an optional response.
func (handler *Handler) Handle(
	ctx    context.Context,
	sess   *session.Session,
	packet *coap.Packet,
) (*coap.Packet, error) {
	if packet == nil {
		return nil, nil
	}

	path := packet.PathSegments()
	if isPing(packet, path) {
		return coap.ResponseFor(packet, coap.CodeChanged, nil), nil
	}

	if isDeviceDescription(packet, path) {
		description, ok := descriptionFromPacket(packet)
		if ok && handler.devices != nil {
			device, err := handler.devices.UpdateDescription(ctx, sess.DeviceID, description)
			if err != nil {
				return nil, err
			}
			handler.queueFirmwareUpdate(packet.MessageID, device)
		}
		return coap.ResponseFor(packet, coap.CodeChanged, nil), nil
	}

	if isEventPublish(packet, path) {
		event, ok := eventFromPacket(sess.DeviceID, packet, path)
		if !ok {
			return coap.ResponseFor(packet, coap.CodeChanged, nil), nil
		}
		if handler.events != nil {
			if _, err := handler.events.Publish(ctx, event); err != nil {
				return nil, err
			}
		}
		return coap.ResponseFor(packet, coap.CodeChanged, nil), nil
	}

	if packet.Type == coap.Confirmable {
		return coap.ResponseFor(packet, coap.CodeEmpty, nil), nil
	}
	return nil, nil
}

// AfterResponse runs work that should happen only after the device sees the ACK.
func (handler *Handler) AfterResponse(
	ctx    context.Context,
	_      *session.Session,
	packet *coap.Packet,
) {
	if handler.firmware == nil || packet == nil {
		return
	}

	handler.mu.Lock()
	device := handler.pendingUpdates[packet.MessageID]
	delete(handler.pendingUpdates, packet.MessageID)
	handler.mu.Unlock()

	if device != nil {
		_, _, _ = handler.firmware.CheckAndStartProductFirmwareUpdate(ctx, device)
	}
}

func (handler *Handler) queueFirmwareUpdate(messageID uint16, device *domain.Device) {
	if handler.firmware == nil || device == nil {
		return
	}

	handler.mu.Lock()
	handler.pendingUpdates[messageID] = device
	handler.mu.Unlock()
}

func isPing(packet *coap.Packet, path []string) bool {
	if packet.Code == coap.CodeEmpty {
		return true
	}
	if len(path) == 0 {
		return packet.Code == coap.CodeGet || packet.Code == coap.CodePost
	}

	first := strings.ToLower(path[0])
	return first == particle.PathPing || first == particle.PathPingShort
}

func isDeviceDescription(packet *coap.Packet, path []string) bool {
	if packet.Code != coap.CodePost && packet.Code != coap.CodePut {
		return false
	}

	if len(path) == 0 {
		_, ok := descriptionFromPacket(packet)
		return ok
	}

	first := strings.ToLower(path[0])
	return first == particle.PathDescribeShort || first == particle.PathDescribe || first == "description" || first == "hello" || first == "h" || first == "attributes" || first == "attrs"
}

func isEventPublish(packet *coap.Packet, path []string) bool {
	if packet.Code != coap.CodePost && packet.Code != coap.CodePut {
		return false
	}
	if len(path) == 0 {
		return false
	}

	first := strings.ToLower(path[0])
	return first == particle.PathEventShort || first == particle.PathEvent || first == particle.PathEvents || first == "publish" || first == "spark"
}

func eventFromPacket(deviceID string, packet *coap.Packet, path []string) (*domain.Event, bool) {
	event := domain.Event{DeviceID: deviceID}

	if len(path) > 1 {
		event.Name = strings.Join(path[1:], "/")
	}

	query := packet.QueryValues()
	if name := firstNonEmpty(query.Get("name"), query.Get("event")); name != "" {
		event.Name = name
	}

	if len(packet.Payload) > 0 {
		if decodeEventPayload(packet.Payload, &event) && event.Name != "" {
			if event.DeviceID == "" {
				event.DeviceID = deviceID
			}
			return &event, true
		}
		if event.Name == "" {
			event.Name = string(packet.Payload)
		} else {
			event.Data = string(packet.Payload)
		}
	}

	if event.Name == "" {
		return nil, false
	}
	if event.DeviceID == "" {
		event.DeviceID = deviceID
	}
	return &event, true
}

func descriptionFromPacket(packet *coap.Packet) (domain.DeviceDescription, bool) {
	if len(packet.Payload) == 0 {
		return domain.DeviceDescription{}, false
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(packet.Payload, &body); err != nil {
		return domain.DeviceDescription{}, false
	}

	description := domain.DeviceDescription{}
	if raw := firstRaw(body, "variables", "variable", "vars", "v", "var"); raw != nil {
		description.Variables = parseVariableMap(raw)
	}
	if raw := firstRaw(body, "functions", "function", "funcs", "f", "fn"); raw != nil {
		description.Functions = parseFunctionList(raw)
	}
	if raw := firstRaw(body, "attributes", "attribute", "attrs", "a", "system", "meta"); raw != nil {
		description.Attributes = parseStringMap(raw)
	}
	mergeTopLevelAttributes(body, &description)

	if len(description.Variables) == 0 && len(description.Functions) == 0 && len(description.Attributes) == 0 {
		return domain.DeviceDescription{}, false
	}
	return description, true
}

func decodeEventPayload(payload []byte, event *domain.Event) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return false
	}
	if raw["name"] == nil && raw["event"] == nil && raw["data"] == nil && raw["deviceID"] == nil && raw["coreid"] == nil {
		return false
	}

	var body struct {
		Name     string `json:"name"`
		Event    string `json:"event"`
		Data     string `json:"data"`
		DeviceID string `json:"deviceID"`
		CoreID   string `json:"coreid"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return false
	}

	if name := firstNonEmpty(body.Name, body.Event); name != "" {
		event.Name = name
	}
	event.Data = body.Data
	if deviceID := firstNonEmpty(body.DeviceID, body.CoreID); deviceID != "" {
		event.DeviceID = deviceID
	}
	return true
}

func firstRaw(body map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if raw, ok := body[key]; ok {
			return raw
		}
	}
	return nil
}

func parseVariableMap(raw json.RawMessage) map[string]string {
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		variables := make(map[string]string, len(names))
		for _, name := range names {
			if name != "" {
				variables[name] = ""
			}
		}
		return variables
	}

	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err == nil {
		variables := make(map[string]string, len(entries))
		for _, entry := range entries {
			name := fmt.Sprint(firstAny(entry, "name", "n", "key"))
			if name != "" && name != "<nil>" {
				variables[name] = typeString(firstAny(entry, "type", "t", "value"))
			}
		}
		return variables
	}

	var stringMap map[string]string
	if err := json.Unmarshal(raw, &stringMap); err == nil && stringMap != nil {
		return stringMap
	}

	var objectMap map[string]any
	if err := json.Unmarshal(raw, &objectMap); err == nil && objectMap != nil {
		variables := make(map[string]string, len(objectMap))
		for name, value := range objectMap {
			variables[name] = typeString(value)
		}
		return variables
	}

	return nil
}

func parseFunctionList(raw json.RawMessage) []string {
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		return cleanStrings(names)
	}

	var objectMap map[string]any
	if err := json.Unmarshal(raw, &objectMap); err == nil && objectMap != nil {
		names := make([]string, 0, len(objectMap))
		for name := range objectMap {
			if name != "" {
				names = append(names, name)
			}
		}
		return names
	}

	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err == nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			name := fmt.Sprint(firstAny(entry, "name", "n", "key"))
			if name != "" && name != "<nil>" {
				names = append(names, name)
			}
		}
		return names
	}

	return nil
}

func parseStringMap(raw json.RawMessage) map[string]string {
	var stringMap map[string]string
	if err := json.Unmarshal(raw, &stringMap); err == nil && stringMap != nil {
		return stringMap
	}

	var objectMap map[string]any
	if err := json.Unmarshal(raw, &objectMap); err != nil || objectMap == nil {
		return nil
	}

	values := make(map[string]string, len(objectMap))
	for key, value := range objectMap {
		values[key] = typeString(value)
	}
	return values
}

func mergeTopLevelAttributes(
	body        map[string]json.RawMessage,
	description *domain.DeviceDescription,
) {
	known := map[string]bool{
		"variables":  true,
		"variable":   true,
		"vars":       true,
		"v":          true,
		"var":        true,
		"functions":  true,
		"function":   true,
		"funcs":      true,
		"f":          true,
		"fn":         true,
		"attributes": true,
		"attribute":  true,
		"attrs":      true,
		"a":          true,
		"system":     true,
		"meta":       true,
	}

	for key, raw := range body {
		if known[key] {
			continue
		}
		if description.Attributes == nil {
			description.Attributes = make(map[string]string)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			description.Attributes[key] = string(raw)
			continue
		}
		description.Attributes[key] = typeString(value)
	}
}

func typeString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		if value, ok := typed["type"]; ok {
			return typeString(value)
		}
		if value, ok := typed["value"]; ok {
			return typeString(value)
		}
		bytes, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(bytes)
	default:
		return fmt.Sprint(typed)
	}
}

func firstAny(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func cleanStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ EventPublisher = (*events.Service)(nil)
