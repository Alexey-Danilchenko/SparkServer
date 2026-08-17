package httpapi

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestNewAppliesHTTPTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		options []Option
		want    Timeouts
	}{
		{
			name: "defaults",
			want: Timeouts{ReadHeader: 5 * time.Second, Read: 15 * time.Second, Idle: 120 * time.Second},
		},
		{
			name: "overrides",
			options: []Option{WithTimeouts(Timeouts{
				ReadHeader: time.Second,
				Read:       2 * time.Second,
				Idle:       3 * time.Second,
			})},
			want: Timeouts{ReadHeader: time.Second, Read: 2 * time.Second, Idle: 3 * time.Second},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := New(":0", Dependencies{}, slog.New(slog.NewTextHandler(io.Discard, nil)), test.options...)
			got := Timeouts{
				ReadHeader: server.server.ReadHeaderTimeout,
				Read:       server.server.ReadTimeout,
				Idle:       server.server.IdleTimeout,
			}
			if got != test.want {
				t.Fatalf("timeouts = %+v, want %+v", got, test.want)
			}
			if server.server.WriteTimeout != 0 {
				t.Fatalf("write timeout = %s, want disabled for SSE", server.server.WriteTimeout)
			}
		})
	}
}

func TestWithTLSAppliesConfiguration(t *testing.T) {
	want := TLSConfig{Enabled: true, CertificateFile: "certificate.pem", PrivateKeyFile: "private-key.pem"}
	server := New(":0", Dependencies{}, nil, WithTLS(want))
	if server.tls != want {
		t.Fatalf("TLS config = %+v, want %+v", server.tls, want)
	}
}
