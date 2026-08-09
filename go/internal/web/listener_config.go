package web

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// DefaultWebListenAddr keeps a fresh local-first installation private until
// its owner deliberately configures both credentials and network access.
const DefaultWebListenAddr = "127.0.0.1:8080"

// WebListenerConfig separates the address serving this process from the
// database-owned address selected for its next start.
type WebListenerConfig struct {
	CurrentAddr       string
	ConfiguredAddr    string
	ExternallyManaged bool
}

// ResolveWebListenerConfig applies the explicit --listen override after the
// persisted user choice. An empty persisted value denotes a first run.
func ResolveWebListenerConfig(persistedAddr, cliAddr string, cliSet bool) (WebListenerConfig, error) {
	configured := strings.TrimSpace(persistedAddr)
	if configured == "" {
		configured = DefaultWebListenAddr
	}
	if err := ValidateWebListenAddr(configured); err != nil {
		return WebListenerConfig{}, fmt.Errorf("configured web listener: %w", err)
	}
	config := WebListenerConfig{CurrentAddr: configured, ConfiguredAddr: configured}
	if cliSet {
		config.CurrentAddr = strings.TrimSpace(cliAddr)
		config.ExternallyManaged = true
		if err := ValidateWebListenAddr(config.CurrentAddr); err != nil {
			return WebListenerConfig{}, fmt.Errorf("--listen: %w", err)
		}
	}
	return config, nil
}

// ValidateWebListenAddr accepts structured TCP bind addresses with a numeric
// non-zero port. Hostnames other than localhost are allowed as explicit
// network binds but are never considered loopback for the safety decision.
func ValidateWebListenAddr(addr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return errors.New("port must be an integer between 1 and 65535")
	}
	if strings.ContainsAny(host, "\r\n\x00") {
		return errors.New("host contains unsupported characters")
	}
	if host != "" && net.ParseIP(host) == nil && !validDNSHostname(host) {
		return errors.New("host must be an IP literal, localhost, or valid DNS hostname")
	}
	return nil
}

func validDNSHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

func webListenerIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return isLoopbackHost(host)
}

// WebListenerExposure reports the conservative exposure classification used by
// startup validation and Settings status.
func WebListenerExposure(addr string) string {
	if webListenerIsLoopback(addr) {
		return "loopback"
	}
	return "network"
}

func validateWebAuthListenerAddr(addr net.Addr, webAccessEnabled bool) error {
	if webAccessEnabled {
		return nil
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr.IP == nil || !tcpAddr.IP.IsLoopback() {
		return errors.New("web access protection is required for a non-loopback listener")
	}
	return nil
}
