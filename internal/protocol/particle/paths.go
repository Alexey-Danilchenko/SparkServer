// Package particle names protocol path aliases used by Particle firmware.
package particle

import (
	"sparkserver/internal/protocol/coap"
)

const (
	PathVariableShort = "v"
	PathVariable      = "variable"
	PathFunctionShort = "f"
	PathFunction      = "function"
	PathDescribeShort = "d"
	PathDescribe      = "describe"
	PathEventShort    = "e"
	PathEvent         = "event"
	PathEvents        = "events"
	PathOTA           = "ota"
	PathFlash         = "flash"
	PathUpdate        = "u"
	PathStart         = "start"
	PathChunkShort    = "c"
	PathChunk         = "chunk"
	PathPingShort     = "p"
	PathPing          = "ping"
	QueryArgument     = "arg"
	QueryArgumentAlt  = "args"
)

// OptionsForPath converts path segments into URI-path CoAP options.
func OptionsForPath(segments ...string) []coap.Option {
	options := make([]coap.Option, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		options = append(options, coap.Option{
			Number: coap.OptionURIPath,
			Value:  []byte(segment),
		})
	}
	return options
}

// OptionsForPathAndQuery adds URI-query options to a path.
func OptionsForPathAndQuery(segments []string, queryValues ...string) []coap.Option {
	options := OptionsForPath(segments...)
	for _, value := range queryValues {
		if value == "" {
			continue
		}
		options = append(options, coap.Option{
			Number: coap.OptionURIQuery,
			Value:  []byte(value),
		})
	}
	return options
}
