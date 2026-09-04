// --- FILE service.https ---

package commands

import (
	"bytes"
	"strings"
	"testing"

	"sprout/internal/types"
)

func TestWriteSafeConfigExposesOnlyEditableValues(t *testing.T) {
	cfg := &types.Configuration{
		LogLevel:  "warn",
		UIBind:    ":8484",
		ProxyBind: "",
		Credentials: []types.Credential{{
			Username: "admin",
			PassHash: "secret-hash",
			PassSalt: "secret-salt",
		}},
		StartCounter: 42,
	}

	var output bytes.Buffer
	writeSafeConfig(&output, cfg)
	got := output.String()
	for _, want := range []string{"log: warn", "ui-bind: :8484", "proxy-bind: disabled"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
	for _, forbidden := range []string{"admin", "secret-hash", "secret-salt", "42"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("output %q exposes internal value %q", got, forbidden)
		}
	}
}

func TestBindWithPort(t *testing.T) {
	tests := []struct {
		name        string
		bind        string
		port        int
		defaultHost string
		want        string
		wantErr     bool
	}{
		{name: "wildcard host", bind: ":8484", port: 9443, want: ":9443"},
		{name: "IPv4 host", bind: "127.0.0.1:8485", port: 9000, want: "127.0.0.1:9000"},
		{name: "IPv6 host", bind: "[::1]:8485", port: 9000, want: "[::1]:9000"},
		{name: "default proxy host", port: 8485, defaultHost: "127.0.0.1", want: "127.0.0.1:8485"},
		{name: "invalid existing bind", bind: "8484", port: 9000, wantErr: true},
		{name: "invalid port", bind: ":8484", port: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bindWithPort(tt.bind, tt.port, tt.defaultHost)
			if (err != nil) != tt.wantErr {
				t.Fatalf("bindWithPort() error = %v, wantErr %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("bindWithPort() = %q, want %q", got, tt.want)
			}
		})
	}
}
