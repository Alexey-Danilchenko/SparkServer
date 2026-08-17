// Package test verifies CoAP packet encoding and decoding.
package coap_test

import (
	"bytes"
	"errors"
	"testing"

	"sparkserver/internal/protocol/coap"
)

func TestCoAPPacketRoundTrip(t *testing.T) {
	packet := coap.Packet{
		Type:      coap.Confirmable,
		Code:      2,
		MessageID: 42,
		Token:     []byte{0xaa, 0xbb},
		Options: []coap.Option{
			{Number: 11, Value: []byte("events")},
			{Number: 12, Value: []byte("json")},
		},
		Payload: []byte(`{"ok":true}`),
	}

	data, err := coap.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}

	parsed, err := coap.Parse(data)
	if err != nil {
		t.Fatalf("parse packet: %v", err)
	}

	if parsed.Version != coap.Version1 {
		t.Fatalf("version = %d", parsed.Version)
	}
	if parsed.Type != coap.Confirmable {
		t.Fatalf("type = %d", parsed.Type)
	}
	if parsed.Code != packet.Code {
		t.Fatalf("code = %d", parsed.Code)
	}
	if parsed.MessageID != packet.MessageID {
		t.Fatalf("message id = %d", parsed.MessageID)
	}
	if !bytes.Equal(parsed.Token, packet.Token) {
		t.Fatalf("token = %x", parsed.Token)
	}
	if len(parsed.Options) != 2 || string(parsed.Options[0].Value) != "events" || string(parsed.Options[1].Value) != "json" {
		t.Fatalf("options = %#v", parsed.Options)
	}
	if !bytes.Equal(parsed.Payload, packet.Payload) {
		t.Fatalf("payload = %q", parsed.Payload)
	}
}

func TestCoAPMarshalSortsOptions(t *testing.T) {
	data, err := coap.Marshal(coap.Packet{
		MessageID: 7,
		Options: []coap.Option{
			{Number: 12, Value: []byte("json")},
			{Number: 11, Value: []byte("events")},
		},
	})
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}

	parsed, err := coap.Parse(data)
	if err != nil {
		t.Fatalf("parse packet: %v", err)
	}
	if parsed.Options[0].Number != 11 || parsed.Options[1].Number != 12 {
		t.Fatalf("options not sorted = %#v", parsed.Options)
	}
}

func TestCoAPExtendedOptionRoundTrip(t *testing.T) {
	value := bytes.Repeat([]byte("x"), 300)
	data, err := coap.Marshal(coap.Packet{
		MessageID: 1,
		Options: []coap.Option{
			{Number: 300, Value: value},
		},
	})
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}

	parsed, err := coap.Parse(data)
	if err != nil {
		t.Fatalf("parse packet: %v", err)
	}
	if len(parsed.Options) != 1 || parsed.Options[0].Number != 300 || !bytes.Equal(parsed.Options[0].Value, value) {
		t.Fatalf("option = %#v", parsed.Options)
	}
}

func TestCoAPRejectsMalformedPackets(t *testing.T) {
	if _, err := coap.Parse([]byte{0x40}); !errors.Is(err, coap.ErrInvalidPacket) {
		t.Fatalf("short packet error = %v", err)
	}

	invalidTokenLength := []byte{0x49, 0x00, 0x00, 0x01}
	if _, err := coap.Parse(invalidTokenLength); !errors.Is(err, coap.ErrInvalidPacket) {
		t.Fatalf("bad token length error = %v", err)
	}

	emptyPayloadMarker := []byte{0x40, 0x00, 0x00, 0x01, 0xff}
	if _, err := coap.Parse(emptyPayloadMarker); !errors.Is(err, coap.ErrInvalidPacket) {
		t.Fatalf("empty payload marker error = %v", err)
	}
}
