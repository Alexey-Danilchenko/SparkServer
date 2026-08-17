// Package tcp accepts device sessions and bridges cloud commands to live devices.
package tcp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"sparkserver/internal/devices"
	"sparkserver/internal/firmware"
	"sparkserver/internal/protocol/coap"
	"sparkserver/internal/protocol/framing"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/session"
)

var (
	// ErrDeviceOffline is returned when no live TCP client exists for a device.
	ErrDeviceOffline = devices.ErrDeviceOffline
	// ErrDeviceTimeout is reserved for timed-out live device commands.
	ErrDeviceTimeout = devices.ErrDeviceTimeout
	// ErrUnexpectedAck reports a device response code that does not match the command.
	ErrUnexpectedAck = errors.New("unexpected device acknowledgement")
)

// Client represents one encrypted TCP session to a Particle-compatible device.
type Client struct {
	deviceID string
	codec    *session.Codec
	writer   *framing.Writer

	writeMutex         sync.Mutex
	pending            map[uint16]chan *coap.Packet
	pendingByToken     map[string]chan *coap.Packet
	pendingMu          sync.Mutex
	nextID             atomic.Uint32
	otaProtocolVersion uint8
	flashSignals       FlashSignalHandler
}

// FlashSignalHandler receives device-originated OTA retry/abort signals.
type FlashSignalHandler interface {
	RetryMissedFlashChunks(ctx context.Context, deviceID string, chunkIndexes []int) (*firmware.FlashJob, error)
	AbortDeviceFlash(ctx context.Context, deviceID string, message string) (*firmware.FlashJob, error)
}

// NewClient creates an encrypted command client for an established device session.
func NewClient(deviceSession *session.Session, stream io.Writer) (*Client, error) {
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		return nil, err
	}

	client := &Client{
		deviceID:       deviceSession.DeviceID,
		codec:          codec,
		writer:         framing.NewWriter(stream),
		pending:        make(map[uint16]chan *coap.Packet),
		pendingByToken: make(map[string]chan *coap.Packet),
	}
	client.nextID.Store(1)
	return client, nil
}

func (client *Client) SetFlashSignalHandler(handler FlashSignalHandler) {
	client.flashSignals = handler
}

// GetVariable sends a live variable read over the encrypted protocol.
func (client *Client) GetVariable(ctx context.Context, variableName string) (string, error) {
	response, err := client.SendRequest(ctx, coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodeGet,
		Options: particle.OptionsForPath(particle.PathVariable, variableName),
	})
	if err != nil {
		return "", err
	}

	return variableValueFromPayload(response.Payload), nil
}

func (client *Client) CallFunction(
	ctx context.Context,
	functionName string,
	argument string,
) (int, error) {
	response, err := client.SendRequest(ctx, coap.Packet{
		Type: coap.Confirmable,
		Code: coap.CodePost,
		Options: particle.OptionsForPathAndQuery(
			[]string{particle.PathFunction, functionName},
			particle.QueryArgument+"="+argument,
		),
		Payload: []byte(argument),
	})
	if err != nil {
		return 0, err
	}

	return functionReturnFromPayload(response.Payload)
}

func (client *Client) Ping(ctx context.Context) error {
	response, err := client.SendRequest(ctx, coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodeGet,
		Options: particle.OptionsForPath(particle.PathPing),
	})
	if err != nil {
		return err
	}
	if response.Code == coap.CodeChanged || response.Code == coap.CodeContent || response.Code == coap.CodeValid || response.Code == coap.CodeEmpty {
		return nil
	}
	return fmt.Errorf("%w: code %d", ErrUnexpectedAck, response.Code)
}

// BeginFlash sends the Particle UpdateBegin payload for an OTA job.
func (client *Client) BeginFlash(ctx context.Context, job *firmware.FlashJob) error {
	payload := updateBeginPayload(job)

	response, err := client.SendRequest(ctx, coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodePost,
		Options: particle.OptionsForPath(particle.PathUpdate),
		Payload: payload,
	})
	if err != nil {
		return err
	}
	if response.Code == coap.CodeChanged || response.Code == coap.CodeContent || response.Code == coap.CodeValid || response.Code == coap.CodeEmpty {
		if len(response.Payload) > 0 {
			client.otaProtocolVersion = response.Payload[0]
		}
		return nil
	}
	return fmt.Errorf("%w: code %d", ErrUnexpectedAck, response.Code)
}

// SendFlashChunk sends one padded firmware chunk with CRC/query metadata.
func (client *Client) SendFlashChunk(
	ctx context.Context,
	job *firmware.FlashJob,
	chunk firmware.OTAChunk,
	data []byte,
) error {
	payload := particleChunkPayload(job, data)
	checksum := crc32.ChecksumIEEE(payload)

	response, err := client.SendRequest(ctx, coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodePost,
		Options: particleChunkOptions(checksum, chunk.Index, client.otaProtocolVersion),
		Payload: payload,
	})
	if err != nil {
		return err
	}
	if response.Code == coap.CodeChanged || response.Code == coap.CodeContent || response.Code == coap.CodeValid || response.Code == coap.CodeEmpty {
		return validateChunkAck(response.Payload, checksum)
	}
	return fmt.Errorf("%w: code %d", ErrUnexpectedAck, response.Code)
}

func (client *Client) CompleteFlash(ctx context.Context, _ *firmware.FlashJob) error {
	return client.SendMessage(ctx, coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodePut,
		Options: particle.OptionsForPath(particle.PathUpdate),
	})
}

func (client *Client) SendRequest(ctx context.Context, packet coap.Packet) (*coap.Packet, error) {
	if packet.MessageID == 0 {
		packet.MessageID = client.nextMessageID()
	}
	if packet.Version == 0 {
		packet.Version = coap.Version1
	}
	if len(packet.Token) == 0 {
		packet.Token = tokenForMessageID(packet.MessageID)
	}

	response := make(chan *coap.Packet, 1)
	client.pendingMu.Lock()
	client.pending[packet.MessageID] = response
	client.pendingByToken[tokenKey(packet.Token)] = response
	client.pendingMu.Unlock()
	defer client.removePending(packet.MessageID, packet.Token)

	if err := client.SendMessage(ctx, packet); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrDeviceTimeout
		}
		return nil, ctx.Err()
	case packet := <-response:
		if packet == nil {
			return nil, ErrDeviceOffline
		}
		return packet, nil
	}
}

func (client *Client) SendMessage(ctx context.Context, packet coap.Packet) error {
	return client.sendPacket(ctx, packet, true)
}

func (client *Client) sendResponse(ctx context.Context, packet coap.Packet) error {
	return client.sendPacket(ctx, packet, false)
}

func (client *Client) sendPacket(ctx context.Context, packet coap.Packet, ensureToken bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if packet.MessageID == 0 {
		packet.MessageID = client.nextMessageID()
	}
	if packet.Version == 0 {
		packet.Version = coap.Version1
	}
	if ensureToken && len(packet.Token) == 0 {
		packet.Token = tokenForMessageID(packet.MessageID)
	}

	plaintext, err := coap.Marshal(packet)
	if err != nil {
		return err
	}

	frame, err := client.codec.Encrypt(plaintext)
	if err != nil {
		return err
	}

	return client.writeFrame(frame)
}

func (client *Client) Codec() *session.Codec {
	return client.codec
}

func (client *Client) HandlePacket(packet *coap.Packet) bool {
	handled, _ := client.HandlePacketWithContext(context.Background(), packet)
	return handled
}

func (client *Client) HandlePacketWithContext(
	ctx context.Context,
	packet *coap.Packet,
) (bool, error) {
	if packet == nil || !isResponsePacket(packet) {
		return client.handleOTAControl(ctx, packet)
	}

	client.pendingMu.Lock()
	response, ok := client.pending[packet.MessageID]
	if !ok {
		response, ok = client.pendingByToken[tokenKey(packet.Token)]
	}
	client.pendingMu.Unlock()
	if !ok {
		return client.handleOTAControl(ctx, packet)
	}

	select {
	case response <- packet:
	default:
	}
	return true, nil
}

func (client *Client) writeFrame(frame []byte) error {
	client.writeMutex.Lock()
	defer client.writeMutex.Unlock()

	return client.writer.WriteFrame(frame)
}

func (client *Client) CloseWithError() {
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()

	for messageID, response := range client.pending {
		close(response)
		delete(client.pending, messageID)
	}
	for token := range client.pendingByToken {
		delete(client.pendingByToken, token)
	}
}

func (client *Client) nextMessageID() uint16 {
	next := client.nextID.Add(1)
	if next > 0xffff {
		client.nextID.Store(1)
		return 1
	}
	return uint16(next)
}

func (client *Client) removePending(messageID uint16, token []byte) {
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()

	delete(client.pending, messageID)
	delete(client.pendingByToken, tokenKey(token))
}

func tokenForMessageID(messageID uint16) []byte {
	return []byte{byte(messageID >> 8), byte(messageID)}
}

func isResponsePacket(packet *coap.Packet) bool {
	return packet.Type == coap.Acknowledgement || packet.Type == coap.Reset || packet.Code >= coap.CodeCreated
}

func (client *Client) handleOTAControl(ctx context.Context, packet *coap.Packet) (bool, error) {
	if packet == nil {
		return false, nil
	}

	path := packet.Path()
	if packet.Code == coap.CodeGet && path == particle.PathChunkShort {
		response := coap.ResponseFor(packet, coap.CodeEmpty, nil)
		if err := client.sendResponse(ctx, *response); err != nil {
			return true, err
		}
		indexes := missedChunkIndexes(packet.Payload)
		if client.flashSignals != nil && len(indexes) > 0 {
			go func() {
				_, _ = client.flashSignals.RetryMissedFlashChunks(ctx, client.deviceID, indexes)
			}()
		}
		return true, nil
	}

	if packet.Code >= coap.CodeBadRequest && client.flashSignals != nil {
		message := strings.TrimSpace(string(packet.Payload))
		if message == "" {
			message = "device aborted flash"
		}
		go func() {
			_, _ = client.flashSignals.AbortDeviceFlash(ctx, client.deviceID, message)
		}()
		return true, nil
	}

	return false, nil
}

func updateBeginPayload(job *firmware.FlashJob) []byte {
	chunkSize := job.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 512
	}

	payload := make([]byte, 12)
	payload[0] = 1
	binary.BigEndian.PutUint16(payload[1:3], uint16(chunkSize))
	binary.BigEndian.PutUint32(payload[3:7], uint32(job.Size))
	payload[7] = 0
	binary.BigEndian.PutUint32(payload[8:12], 0)
	return payload
}

func particleChunkPayload(job *firmware.FlashJob, data []byte) []byte {
	chunkSize := job.ChunkSize
	if chunkSize <= 0 || len(data) >= chunkSize {
		return append([]byte(nil), data...)
	}

	payload := make([]byte, chunkSize)
	copy(payload, data)
	return payload
}

func particleChunkOptions(checksum uint32, chunkIndex int, protocolVersion uint8) []coap.Option {
	crc := make([]byte, 4)
	binary.BigEndian.PutUint32(crc, checksum)
	options := particle.OptionsForPath(particle.PathChunkShort)
	options = append(options, coap.Option{Number: coap.OptionURIQuery, Value: crc})
	if protocolVersion > 0 {
		index := make([]byte, 2)
		binary.BigEndian.PutUint16(index, uint16(chunkIndex))
		options = append(options, coap.Option{Number: coap.OptionURIQuery, Value: index})
	}
	return options
}

func validateChunkAck(payload []byte, checksum uint32) error {
	if len(payload) < 4 {
		return nil
	}
	if binary.BigEndian.Uint32(payload[:4]) != checksum {
		return fmt.Errorf("%w: chunk crc mismatch", ErrUnexpectedAck)
	}
	return nil
}

func tokenKey(token []byte) string {
	return string(token)
}

func missedChunkIndexes(payload []byte) []int {
	indexes := make([]int, 0, len(payload)/2)
	for offset := 0; offset+1 < len(payload); offset += 2 {
		indexes = append(indexes, int(binary.BigEndian.Uint16(payload[offset:offset+2])))
	}
	return indexes
}

func variableValueFromPayload(payload []byte) string {
	var body struct {
		Result any `json:"result"`
		Value  any `json:"value"`
	}
	if err := json.Unmarshal(payload, &body); err == nil {
		if body.Result != nil {
			return fmt.Sprint(body.Result)
		}
		if body.Value != nil {
			return fmt.Sprint(body.Value)
		}
	}
	if len(payload) == 4 {
		return strconv.Itoa(int(binary.BigEndian.Uint32(payload)))
	}
	return string(payload)
}

func functionReturnFromPayload(payload []byte) (int, error) {
	var body struct {
		ReturnValue *int `json:"return_value"`
		Result      *int `json:"result"`
	}
	if err := json.Unmarshal(payload, &body); err == nil {
		if body.ReturnValue != nil {
			return *body.ReturnValue, nil
		}
		if body.Result != nil {
			return *body.Result, nil
		}
	}

	if len(payload) == 4 {
		return int(binary.BigEndian.Uint32(payload)), nil
	}

	value := strings.TrimSpace(string(payload))
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}
