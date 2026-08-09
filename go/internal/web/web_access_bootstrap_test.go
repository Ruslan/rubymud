package web

import (
	"net"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"rubymud/go/internal/storage"
)

func testWebAuthHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return string(hash)
}

func TestResolveWebListenerConfigDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name      string
		persisted string
		cli       string
		cliSet    bool
		want      WebListenerConfig
	}{
		{
			name: "first run is loopback only",
			want: WebListenerConfig{CurrentAddr: DefaultWebListenAddr, ConfiguredAddr: DefaultWebListenAddr},
		},
		{
			name:      "persisted listener is used on next start",
			persisted: "0.0.0.0:8080",
			want:      WebListenerConfig{CurrentAddr: "0.0.0.0:8080", ConfiguredAddr: "0.0.0.0:8080"},
		},
		{
			name:      "ipv6 loopback remains local",
			persisted: "[::1]:8080",
			want:      WebListenerConfig{CurrentAddr: "[::1]:8080", ConfiguredAddr: "[::1]:8080"},
		},
		{
			name:      "explicit listener owns current start",
			persisted: "0.0.0.0:8080",
			cli:       "127.0.0.1:9000",
			cliSet:    true,
			want:      WebListenerConfig{CurrentAddr: "127.0.0.1:9000", ConfiguredAddr: "0.0.0.0:8080", ExternallyManaged: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveWebListenerConfig(tc.persisted, tc.cli, tc.cliSet)
			if err != nil {
				t.Fatalf("ResolveWebListenerConfig: %v", err)
			}
			if got != tc.want {
				t.Fatalf("config = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestWebListenerExposureClassifiesLoopbackAndWildcard(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want string
	}{
		{addr: "127.0.0.1:8080", want: "loopback"},
		{addr: "[::1]:8080", want: "loopback"},
		{addr: "localhost:8080", want: "loopback"},
		{addr: ":8080", want: "network"},
		{addr: "0.0.0.0:8080", want: "network"},
		{addr: "[::]:8080", want: "network"},
	} {
		if got := WebListenerExposure(tc.addr); got != tc.want {
			t.Fatalf("WebListenerExposure(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestResolveWebListenerConfigRejectsInvalidAddresses(t *testing.T) {
	for _, addr := range []string{"", "localhost", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http", "http://127.0.0.1:8080", "bad host:8080", "foo/bar:8080", "-bad.example:8080", "bad-.example:8080", "bad..example:8080"} {
		if _, err := ResolveWebListenerConfig(DefaultWebListenAddr, addr, true); err == nil {
			t.Fatalf("ResolveWebListenerConfig(%q) succeeded", addr)
		}
	}
}

func TestValidateWebListenAddrAcceptsStructuredHosts(t *testing.T) {
	for _, addr := range []string{":8080", "127.0.0.1:8080", "[::1]:8080", "localhost:8080", "mud.example.test:8080", "a-b.c9.example:65535"} {
		if err := ValidateWebListenAddr(addr); err != nil {
			t.Fatalf("ValidateWebListenAddr(%q) = %v", addr, err)
		}
	}
}

func TestWebAuthRefusesNetworkListenerWithoutProtection(t *testing.T) {
	base, _ := setupTestServer(t)
	unsafe, err := New("0.0.0.0:0", base.manager, base.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New unsafe listener: %v", err)
	}
	t.Cleanup(func() { _ = unsafe.Close() })
	if err := unsafe.ListenAndServe(); err == nil {
		t.Fatal("network listener without web access protection was served")
	}

	settings := storage.WebAuthSettings{Enabled: true, Username: "operator", PasswordHash: testWebAuthHash(t, "correct horse battery staple")}
	if _, err := base.store.SaveWebAuthSettings(settings); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
	server, err := NewWithWebListenerConfig(WebListenerConfig{CurrentAddr: "0.0.0.0:8080", ConfiguredAddr: "0.0.0.0:8080"}, base.manager, base.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("protected network listener rejected: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
}

func TestValidateWebAuthBoundListenerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		addr    net.Addr
		enabled bool
		wantErr bool
	}{
		{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, enabled: false},
		{addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}, enabled: false},
		{addr: &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8080}, enabled: false, wantErr: true},
		{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8080}, enabled: false, wantErr: true},
		{addr: &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8080}, enabled: true},
	} {
		err := validateWebAuthListenerAddr(tc.addr, tc.enabled)
		if (err != nil) != tc.wantErr {
			t.Fatalf("validateWebAuthListenerAddr(%v, %v) error = %v, wantErr=%v", tc.addr, tc.enabled, err, tc.wantErr)
		}
	}
}
