package s0_discovery

import (
	"context"
	"fmt"
	"net"
	"time"
)

// probeIPMI attempts to detect an IPMI BMC by checking if UDP port 623 responds.
// IPMI provides limited system info compared to Redfish. We detect the BMC
// and mark it for further inspection after OS boot.
func probeIPMI(ctx context.Context, addr string, creds BMCCredentials) (*DiscoveredNode, error) {
	_ = creds // IPMI presence ping does not require authentication

	// Open a UDP socket to the BMC. Note: UDP dial always succeeds locally;
	// actual BMC detection happens via the ASF Presence Ping below.
	conn, err := net.DialTimeout("udp", addr+":623", 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("IPMI port 623 not reachable on %s", addr)
	}
	defer conn.Close()

	// Send IPMI Get Channel Auth Capabilities command (ASF Presence Ping)
	// This is the standard way to detect IPMI BMCs via RMCP.
	asfPing := []byte{
		0x06, 0x00, 0xff, 0x06, // RMCP header
		0x00, 0x00, 0x11, 0xbe, // ASF header
		0x80, 0x00, 0x00, 0x00, // IANA Enterprise Number (ASF)
		0x00, 0x00, 0x00, 0x00, // Message Type = Presence Ping
	}

	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return nil, fmt.Errorf("setting deadline for %s: %w", addr, err)
	}

	_, err = conn.Write(asfPing)
	if err != nil {
		return nil, fmt.Errorf("IPMI write failed on %s: %w", addr, err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("no IPMI response from %s", addr)
	}

	// Got a response — this is likely an IPMI BMC
	return &DiscoveredNode{
		BMCAddress: addr,
		Protocol:   "ipmi",
		PowerState: "unknown", // IPMI presence ping doesn't report power state
	}, nil
}
