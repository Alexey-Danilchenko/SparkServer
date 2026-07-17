// Package coap provides the compact CoAP-like packet model used by Particle devices.
package coap

import (
	"net/url"
	"strings"
)

const (
	CodeEmpty      uint8 = 0
	CodeGet        uint8 = 1
	CodePost       uint8 = 2
	CodePut        uint8 = 3
	CodeDelete     uint8 = 4
	CodeCreated    uint8 = 65
	CodeDeleted    uint8 = 66
	CodeValid      uint8 = 67
	CodeChanged    uint8 = 68
	CodeContent    uint8 = 69
	CodeBadRequest uint8 = 128

	OptionURIPath  uint16 = 11
	OptionURIQuery uint16 = 15
)

// PathSegments extracts URI path options in protocol order.
func (packet Packet) PathSegments() []string {
	segments := make([]string, 0)
	for _, option := range packet.Options {
		if option.Number != OptionURIPath {
			continue
		}

		value := string(option.Value)
		if value == "" {
			continue
		}
		segments = append(segments, value)
	}
	return segments
}

func (packet Packet) Path() string {
	return strings.Join(packet.PathSegments(), "/")
}

func (packet Packet) QueryValues() url.Values {
	values := url.Values{}
	for _, option := range packet.Options {
		if option.Number != OptionURIQuery {
			continue
		}

		key, value, ok := strings.Cut(string(option.Value), "=")
		if ok {
			values.Add(key, value)
			continue
		}
		values.Add(key, "")
	}
	return values
}

// ResponseFor creates an acknowledgement/non-confirmable response matching a request.
func ResponseFor(request *Packet, code uint8, payload []byte) *Packet {
	responseType := Acknowledgement
	if request.Type == NonConfirmable {
		responseType = NonConfirmable
	}

	token := append([]byte(nil), request.Token...)
	return &Packet{
		Version:   Version1,
		Type:      responseType,
		Code:      code,
		MessageID: request.MessageID,
		Token:     token,
		Payload:   append([]byte(nil), payload...),
	}
}
