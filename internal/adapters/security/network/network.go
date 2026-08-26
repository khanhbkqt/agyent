package network

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

var (
	cloudMetadataCIDRs = []net.IPNet{
		*mustCIDR("169.254.169.254/32"), // AWS / GCP / Azure metadata
		*mustCIDR("100.100.100.200/32"), // Alibaba Cloud metadata
	}

	privateCIDRs = []net.IPNet{
		*mustCIDR("0.0.0.0/8"),      // Current network / Linux localhost alias
		*mustCIDR("127.0.0.0/8"),    // IPv4 Loopback
		*mustCIDR("10.0.0.0/8"),     // RFC 1918 Class A
		*mustCIDR("172.16.0.0/12"),  // RFC 1918 Class B
		*mustCIDR("192.168.0.0/16"), // RFC 1918 Class C
		*mustCIDR("169.254.0.0/16"), // Link-Local
		*mustCIDR("100.64.0.0/10"),  // Carrier-Grade NAT / Tailscale / AWS VPC internal
		*mustCIDR("::1/128"),        // IPv6 Loopback
		*mustCIDR("::/128"),         // IPv6 Unspecified
		*mustCIDR("fc00::/7"),       // IPv6 Unique Local
		*mustCIDR("fe80::/10"),      // IPv6 Link-Local
	}
)

func mustCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return ipnet
}

func parseCIDR(s string) (net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return net.IPNet{}, err
	}
	return *ipnet, nil
}

// Evaluator checks target URLs for SSRF, metadata leaks, and private network exfiltration.
type Evaluator struct {
	blockMetadata        bool
	blockPrivateNetworks bool
	preventRebinding     bool
}

// NewEvaluator constructs a network security evaluator.
func NewEvaluator(cfg config.NetworkGuardrailConfig) *Evaluator {
	return &Evaluator{
		blockMetadata:        cfg.BlockCloudMetadata,
		blockPrivateNetworks: cfg.BlockPrivateNetworks,
		preventRebinding:     cfg.PreventDNSRebinding,
	}
}

// EvaluateURL parses and verifies destination URL safety.
func (e *Evaluator) EvaluateURL(rawURL string) (domain.SecurityDecision, error) {
	if rawURL == "" {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   "Target URL cannot be empty",
		}, nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("Invalid URL format: %v", err),
		}, nil
	}

	host := parsed.Hostname()
	if host == "" {
		host = parsed.Host
	}

	// 1. Check known metadata hostnames
	lowerHost := strings.ToLower(host)
	if e.blockMetadata {
		if lowerHost == "metadata.google.internal" ||
			lowerHost == "instance-data" ||
			strings.Contains(lowerHost, "169.254.169.254") ||
			strings.Contains(lowerHost, "100.100.100.200") {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [SSRF Guardrail]: Direct access to Cloud Instance Metadata is forbidden",
			}, nil
		}
	}

	// 2. Normalize host if given as numeric integer/hex/octal/shorthand IP
	if parsedIP := parseIntegerOrEncodedIP(lowerHost); parsedIP != nil {
		if decision := e.checkIP(parsedIP); decision.Decision == domain.DecisionDeny {
			return decision, nil
		}
	}

	// 3. Direct IP check
	if ip := net.ParseIP(host); ip != nil {
		if decision := e.checkIP(ip); decision.Decision == domain.DecisionDeny {
			return decision, nil
		}
	} else if e.preventRebinding {
		// 4. DNS Resolution & Rebinding Check
		ips, err := net.LookupIP(host)
		if err != nil {
			if e.blockPrivateNetworks || e.preventRebinding {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [SSRF Guardrail]: Failed to securely resolve destination host '%s': %v", host, err),
				}, nil
			}
			return domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Host resolution passed to substrate",
			}, nil
		}

		for _, resolvedIP := range ips {
			if decision := e.checkIP(resolvedIP); decision.Decision == domain.DecisionDeny {
				return decision, nil
			}
		}
	}

	return domain.SecurityDecision{
		Decision: domain.DecisionAllow,
		Reason:   "Destination network address verified",
	}, nil
}

func (e *Evaluator) checkIP(ip net.IP) domain.SecurityDecision {
	// Normalize IPv4-mapped IPv6 (::ffff:127.0.0.1 -> 127.0.0.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	// Cloud metadata check
	if e.blockMetadata {
		for _, metaCIDR := range cloudMetadataCIDRs {
			if metaCIDR.Contains(ip) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [SSRF Guardrail]: Request to Cloud Metadata IP '%s' is strictly forbidden", ip.String()),
				}
			}
		}
	}

	// Private network check
	if e.blockPrivateNetworks {
		for _, netRange := range privateCIDRs {
			if netRange.Contains(ip) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [SSRF Guardrail]: Egress to private network IP '%s' (%s) is forbidden", ip.String(), netRange.String()),
				}
			}
		}
	}

	return domain.SecurityDecision{Decision: domain.DecisionAllow}
}

func parseIntegerOrEncodedIP(host string) net.IP {
	// Check pure decimal integer representation e.g. 2130706433
	if val, err := strconv.ParseUint(host, 10, 32); err == nil {
		return net.IPv4(byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
	}
	// Check pure hex representation e.g. 0x7f000001
	if strings.HasPrefix(host, "0x") || strings.HasPrefix(host, "0X") {
		if val, err := strconv.ParseUint(host[2:], 16, 32); err == nil {
			return net.IPv4(byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
		}
	}
	// Check dotted octal/hex/shorthand notation (e.g. 0177.0.0.1, 127.1)
	parts := strings.Split(host, ".")
	if len(parts) >= 2 && len(parts) <= 4 {
		var bytes [4]byte
		valid := true
		for i, p := range parts {
			if p == "" {
				valid = false
				break
			}
			// Parse with base 0 (supports decimal, 0-prefix octal, 0x-prefix hex)
			v, err := strconv.ParseUint(p, 0, 32)
			if err != nil {
				valid = false
				break
			}
			if i == len(parts)-1 && len(parts) < 4 {
				// Shorthand handling: last component absorbs remaining bytes
				switch len(parts) {
				case 2: // A.B e.g. 127.1 -> 127.0.0.1
					bytes[0] = byte(partsVal(parts[0]))
					bytes[1] = byte(v >> 16)
					bytes[2] = byte(v >> 8)
					bytes[3] = byte(v)
				case 3: // A.B.C e.g. 127.0.1 -> 127.0.0.1
					bytes[0] = byte(partsVal(parts[0]))
					bytes[1] = byte(partsVal(parts[1]))
					bytes[2] = byte(v >> 8)
					bytes[3] = byte(v)
				}
				return net.IPv4(bytes[0], bytes[1], bytes[2], bytes[3])
			}
			if v > 255 {
				valid = false
				break
			}
			bytes[i] = byte(v)
		}
		if valid && len(parts) == 4 {
			return net.IPv4(bytes[0], bytes[1], bytes[2], bytes[3])
		}
	}
	return nil
}

func partsVal(s string) uint64 {
	v, _ := strconv.ParseUint(s, 0, 32)
	return v
}
