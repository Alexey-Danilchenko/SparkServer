// Package test verifies TCP frame encoding and bounds checks.
package framing_test

import (
	"bytes"
	"errors"
	"testing"

	"sparkserver/internal/protocol/framing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	writer := framing.NewWriter(&buffer)
	reader := framing.NewReader(&buffer, 1024)

	if err := writer.WriteFrame([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	frame, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if !bytes.Equal(frame, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("frame = %x", frame)
	}
}

func TestFrameReaderRejectsOversizeFrame(t *testing.T) {
	buffer := bytes.NewBuffer([]byte{0x00, 0x04, 0x01, 0x02, 0x03, 0x04})
	reader := framing.NewReader(buffer, 3)

	if _, err := reader.ReadFrame(); !errors.Is(err, framing.ErrFrameTooLarge) {
		t.Fatalf("error = %v", err)
	}
}
