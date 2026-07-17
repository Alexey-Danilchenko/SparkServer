// Package collider provides virtual Particle device helpers for integration tests.
package collider

import (
	"net"
	"testing"
	"time"

	"sparkserver/internal/protocol/coap"
	"sparkserver/internal/protocol/framing"
	"sparkserver/internal/protocol/handshake"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/session"
)

type Device struct {
	t          *testing.T
	conn       net.Conn
	deviceID   string
	sessionKey []byte
	codec      *session.Codec
	reader     *framing.Reader
	writer     *framing.Writer
	nextID     uint16
}

func New(t *testing.T, conn net.Conn, deviceID string) *Device {
	t.Helper()

	sessionKey := []byte("0123456789abcdef")
	deviceSession := &session.Session{DeviceID: deviceID, SessionKey: sessionKey}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	return &Device{
		t:          t,
		conn:       conn,
		deviceID:   deviceID,
		sessionKey: sessionKey,
		codec:      codec,
		reader:     framing.NewReader(conn, framing.DefaultMaxFrameSize),
		writer:     framing.NewWriter(conn),
		nextID:     1,
	}
}

func (device *Device) Handshake(keyManager *protocolkeys.Manager) {
	device.t.Helper()

	publicKey, err := keyManager.LoadServerPublicKey()
	if err != nil {
		device.t.Fatalf("load server public key: %v", err)
	}

	encrypted, err := protocolkeys.EncryptPKCS1v15(publicKey, device.sessionKey)
	if err != nil {
		device.t.Fatalf("encrypt session key: %v", err)
	}

	payload, err := handshake.MarshalRequest(handshake.Request{
		DeviceID:            device.deviceID,
		EncryptedSessionKey: encrypted,
	})
	if err != nil {
		device.t.Fatalf("marshal handshake: %v", err)
	}
	if err := framing.NewWriter(device.conn).WriteFrame(payload); err != nil {
		device.t.Fatalf("write handshake: %v", err)
	}
}

func (device *Device) Describe(payload string) *coap.Packet {
	return device.Send(coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodePut,
		Options: particle.OptionsForPath(particle.PathDescribeShort),
		Payload: []byte(payload),
	})
}

func (device *Device) Publish(eventName string, data string) *coap.Packet {
	return device.Send(coap.Packet{
		Type:    coap.Confirmable,
		Code:    coap.CodePost,
		Options: particle.OptionsForPath(particle.PathEvents, eventName),
		Payload: []byte(data),
	})
}

func (device *Device) Send(packet coap.Packet) *coap.Packet {
	device.t.Helper()

	if packet.MessageID == 0 {
		packet.MessageID = device.nextMessageID()
	}
	if packet.Version == 0 {
		packet.Version = coap.Version1
	}
	if len(packet.Token) == 0 {
		packet.Token = []byte{byte(packet.MessageID >> 8), byte(packet.MessageID)}
	}

	device.WritePacket(packet)
	return device.ReadPacket()
}

func (device *Device) ReadRequest() *coap.Packet {
	device.t.Helper()
	return device.ReadPacket()
}

func (device *Device) Respond(request *coap.Packet, code uint8, payload []byte) {
	device.t.Helper()

	response := coap.ResponseFor(request, code, payload)
	device.WritePacket(*response)
}

func (device *Device) WritePacket(packet coap.Packet) {
	device.t.Helper()

	plaintext, err := coap.Marshal(packet)
	if err != nil {
		device.t.Fatalf("marshal packet: %v", err)
	}
	frame, err := device.codec.Encrypt(plaintext)
	if err != nil {
		device.t.Fatalf("encrypt packet: %v", err)
	}
	if err := device.writer.WriteFrame(frame); err != nil {
		device.t.Fatalf("write packet: %v", err)
	}
}

func (device *Device) ReadPacket() *coap.Packet {
	device.t.Helper()

	if err := device.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		device.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := device.reader.ReadFrame()
	if err != nil {
		device.t.Fatalf("read frame: %v", err)
	}
	plaintext, err := device.codec.Decrypt(frame)
	if err != nil {
		device.t.Fatalf("decrypt frame: %v", err)
	}
	packet, err := coap.Parse(plaintext)
	if err != nil {
		device.t.Fatalf("parse packet: %v", err)
	}
	return packet
}

func (device *Device) Close() {
	_ = device.conn.Close()
}

func (device *Device) nextMessageID() uint16 {
	messageID := device.nextID
	device.nextID++
	return messageID
}

func WaitUntil(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
