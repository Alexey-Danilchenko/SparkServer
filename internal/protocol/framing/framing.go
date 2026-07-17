// Package framing reads and writes length-prefixed TCP frames.
package framing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// DefaultMaxFrameSize bounds device frames before decryption/parsing.
const DefaultMaxFrameSize = 64 * 1024

// ErrFrameTooLarge prevents unbounded memory allocation from malformed peers.
var ErrFrameTooLarge = errors.New("frame too large")

// Reader consumes two-byte big-endian length-prefixed frames.
type Reader struct {
	reader io.Reader
	max    uint32
}

// Writer emits two-byte big-endian length-prefixed frames.
type Writer struct {
	writer io.Writer
}

// NewReader creates a bounded frame reader.
func NewReader(reader io.Reader, maxFrameSize uint32) *Reader {
	if maxFrameSize == 0 {
		maxFrameSize = DefaultMaxFrameSize
	}

	return &Reader{
		reader: reader,
		max:    maxFrameSize,
	}
}

func NewWriter(writer io.Writer) *Writer {
	return &Writer{writer: writer}
}

// ReadFrame reads exactly one length-prefixed frame.
func (reader *Reader) ReadFrame() ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader.reader, header[:]); err != nil {
		return nil, err
	}

	size := uint32(binary.BigEndian.Uint16(header[:]))
	if size > reader.max {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, size, reader.max)
	}

	frame := make([]byte, size)
	if _, err := io.ReadFull(reader.reader, frame); err != nil {
		return nil, err
	}

	return frame, nil
}

// WriteFrame writes exactly one length-prefixed frame.
func (writer *Writer) WriteFrame(frame []byte) error {
	if len(frame) > 0xffff {
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(frame), 0xffff)
	}

	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(frame)))
	if _, err := writer.writer.Write(header[:]); err != nil {
		return err
	}

	_, err := writer.writer.Write(frame)
	return err
}
