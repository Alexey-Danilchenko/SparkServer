// Package coap marshals and parses the Particle-flavored CoAP message subset.
package coap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	Version1      = 1
	MaxTokenSize  = 8
	payloadMarker = 0xff
)

// ErrInvalidPacket identifies malformed or unsupported CoAP packets.
var ErrInvalidPacket = errors.New("invalid CoAP packet")

// Type is the CoAP message type nibble.
type Type uint8

const (
	Confirmable Type = iota
	NonConfirmable
	Acknowledgement
	Reset
)

// Packet is the decrypted protocol message exchanged with a device.
type Packet struct {
	Version   uint8
	Type      Type
	Code      uint8
	MessageID uint16
	Token     []byte
	Options   []Option
	Payload   []byte
}

// Option stores one CoAP option after delta decoding.
type Option struct {
	Number uint16
	Value  []byte
}

// Marshal encodes a packet, sorting options as required by CoAP delta encoding.
func Marshal(packet Packet) ([]byte, error) {
	if packet.Version == 0 {
		packet.Version = Version1
	}
	if packet.Version != Version1 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidPacket, packet.Version)
	}
	if len(packet.Token) > MaxTokenSize {
		return nil, fmt.Errorf("%w: token too long", ErrInvalidPacket)
	}

	options := append([]Option(nil), packet.Options...)
	sort.Slice(options, func(left int, right int) bool {
		return options[left].Number < options[right].Number
	})

	var buffer bytes.Buffer
	first := byte(packet.Version<<6) | byte(packet.Type&0x03)<<4 | byte(len(packet.Token))
	buffer.WriteByte(first)
	buffer.WriteByte(packet.Code)
	if err := binary.Write(&buffer, binary.BigEndian, packet.MessageID); err != nil {
		return nil, err
	}
	buffer.Write(packet.Token)

	var previous uint16
	for _, option := range options {
		if option.Number < previous {
			return nil, fmt.Errorf("%w: options out of order", ErrInvalidPacket)
		}

		delta := option.Number - previous
		if err := writeOption(&buffer, delta, option.Value); err != nil {
			return nil, err
		}
		previous = option.Number
	}

	if len(packet.Payload) > 0 {
		buffer.WriteByte(payloadMarker)
		buffer.Write(packet.Payload)
	}

	return buffer.Bytes(), nil
}

// Parse decodes a CoAP packet from decrypted session bytes.
func Parse(data []byte) (*Packet, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("%w: too short", ErrInvalidPacket)
	}

	first := data[0]
	version := first >> 6
	tokenLength := int(first & 0x0f)
	if version != Version1 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidPacket, version)
	}
	if tokenLength > MaxTokenSize {
		return nil, fmt.Errorf("%w: token too long", ErrInvalidPacket)
	}
	if len(data) < 4+tokenLength {
		return nil, fmt.Errorf("%w: truncated token", ErrInvalidPacket)
	}

	packet := &Packet{
		Version:   version,
		Type:      Type((first >> 4) & 0x03),
		Code:      data[1],
		MessageID: binary.BigEndian.Uint16(data[2:4]),
		Token:     append([]byte(nil), data[4:4+tokenLength]...),
	}

	offset := 4 + tokenLength
	var previous uint16
	for offset < len(data) {
		if data[offset] == payloadMarker {
			offset++
			if offset == len(data) {
				return nil, fmt.Errorf("%w: empty payload marker", ErrInvalidPacket)
			}
			packet.Payload = append([]byte(nil), data[offset:]...)
			return packet, nil
		}

		option, consumed, err := parseOption(data[offset:], previous)
		if err != nil {
			return nil, err
		}
		packet.Options = append(packet.Options, option)
		previous = option.Number
		offset += consumed
	}

	return packet, nil
}

func writeOption(buffer *bytes.Buffer, delta uint16, value []byte) error {
	deltaNibble, deltaExtra, err := encodeExtended(delta)
	if err != nil {
		return err
	}
	lengthNibble, lengthExtra, err := encodeExtended(uint16(len(value)))
	if err != nil {
		return err
	}

	buffer.WriteByte(byte(deltaNibble<<4 | lengthNibble))
	buffer.Write(deltaExtra)
	buffer.Write(lengthExtra)
	buffer.Write(value)
	return nil
}

func parseOption(data []byte, previous uint16) (Option, int, error) {
	if len(data) == 0 {
		return Option{}, 0, fmt.Errorf("%w: missing option header", ErrInvalidPacket)
	}

	header := data[0]
	offset := 1

	delta, consumed, err := decodeExtended(header>>4, data[offset:])
	if err != nil {
		return Option{}, 0, err
	}
	offset += consumed

	length, consumed, err := decodeExtended(header&0x0f, data[offset:])
	if err != nil {
		return Option{}, 0, err
	}
	offset += consumed

	if len(data) < offset+int(length) {
		return Option{}, 0, fmt.Errorf("%w: truncated option value", ErrInvalidPacket)
	}

	number := previous + delta
	return Option{
		Number: number,
		Value:  append([]byte(nil), data[offset:offset+int(length)]...),
	}, offset + int(length), nil
}

func encodeExtended(value uint16) (byte, []byte, error) {
	switch {
	case value < 13:
		return byte(value), nil, nil
	case value < 269:
		return 13, []byte{byte(value - 13)}, nil
	default:
		adjusted := value - 269
		return 14, []byte{byte(adjusted >> 8), byte(adjusted)}, nil
	}
}

func decodeExtended(nibble byte, data []byte) (uint16, int, error) {
	switch nibble {
	case 15:
		return 0, 0, fmt.Errorf("%w: reserved option nibble", ErrInvalidPacket)
	case 14:
		if len(data) < 2 {
			return 0, 0, fmt.Errorf("%w: truncated extended option", ErrInvalidPacket)
		}
		return binary.BigEndian.Uint16(data[:2]) + 269, 2, nil
	case 13:
		if len(data) < 1 {
			return 0, 0, fmt.Errorf("%w: truncated extended option", ErrInvalidPacket)
		}
		return uint16(data[0]) + 13, 1, nil
	default:
		return uint16(nibble), 0, nil
	}
}
