package network

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Destination is one TCP authority a guest may reach through the host proxy.
// Host is a DNS name, never an IP address or a wildcard.
type Destination struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

// Policy is prepared by the host for one run. An empty policy permits no
// external connections and does not attach a network device to normal runs.
type Policy struct {
	TCP []Destination `json:"tcp,omitempty"`
}

func (p Policy) Enabled() bool { return len(p.TCP) != 0 }

func ParseDestination(authority string) (Destination, error) {
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		return Destination{}, fmt.Errorf("network destination must be HOST:PORT: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return Destination{}, fmt.Errorf("invalid network port %q", portText)
	}
	destination := Destination{Host: host, Port: uint16(port)}
	if err := validateDestination(destination); err != nil {
		return Destination{}, err
	}
	return destination, nil
}

func (p Policy) Validate() error {
	if len(p.TCP) > 64 {
		return fmt.Errorf("network policy exceeds 64 destinations")
	}
	seen := make(map[Destination]bool, len(p.TCP))
	for _, destination := range p.TCP {
		if err := validateDestination(destination); err != nil {
			return err
		}
		if seen[destination] {
			return fmt.Errorf("duplicate network destination %s:%d", destination.Host, destination.Port)
		}
		seen[destination] = true
	}
	return nil
}

func validateDestination(d Destination) error {
	if d.Port == 0 || len(d.Host) == 0 || len(d.Host) > 253 ||
		d.Host != strings.ToLower(d.Host) || net.ParseIP(d.Host) != nil {
		return fmt.Errorf("invalid network destination %q:%d", d.Host, d.Port)
	}
	for _, label := range strings.Split(d.Host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid network hostname %q", d.Host)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return fmt.Errorf("invalid network hostname %q", d.Host)
			}
		}
	}
	if !strings.Contains(d.Host, ".") {
		return fmt.Errorf("network hostname must be fully qualified: %q", d.Host)
	}
	return nil
}
