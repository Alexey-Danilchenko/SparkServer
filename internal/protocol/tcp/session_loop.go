// Package tcp contains the encrypted per-device session loop.
package tcp

import (
	"context"
	"io"

	"sparkserver/internal/protocol/coap"
	"sparkserver/internal/protocol/framing"
	"sparkserver/internal/protocol/session"
)

// ServeSession decrypts frames, dispatches CoAP packets, and writes encrypted replies.
func ServeSession(
	ctx context.Context,
	stream io.ReadWriter,
	deviceSession *session.Session,
	client *Client,
	handler MessageHandler,
	onPacket func(deviceID string),
) error {
	codec := client.Codec()
	reader := framing.NewReader(stream, framing.DefaultMaxFrameSize)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		frame, err := reader.ReadFrame()
		if err != nil {
			return err
		}

		plaintext, err := codec.Decrypt(frame)
		if err != nil {
			return err
		}

		packet, err := coap.Parse(plaintext)
		if err != nil {
			return err
		}
		if onPacket != nil {
			onPacket(deviceSession.DeviceID)
		}
		handled, err := client.HandlePacketWithContext(ctx, packet)
		if err != nil {
			return err
		}
		if handled {
			continue
		}

		response, err := handler.Handle(ctx, deviceSession, packet)
		if err != nil {
			return err
		}
		if response == nil {
			continue
		}

		responsePlaintext, err := coap.Marshal(*response)
		if err != nil {
			return err
		}
		responseFrame, err := codec.Encrypt(responsePlaintext)
		if err != nil {
			return err
		}
		if err := client.writeFrame(responseFrame); err != nil {
			return err
		}
		if afterResponse, ok := handler.(AfterResponseHandler); ok {
			go afterResponse.AfterResponse(ctx, deviceSession, packet)
		}
	}
}
