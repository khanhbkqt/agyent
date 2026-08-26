package evolution

import (
	"errors"
	"regexp"
	"strings"

	"agyent/internal/core/ports"
)

var _ ports.SecurityGuardrailPort = (*SecurityGuardrail)(nil)

// ErrPromptInjectionDetected is returned when malicious system overriding commands are detected.
var ErrPromptInjectionDetected = errors.New("security guardrail: prompt injection or system override detected")

// SecurityGuardrail handles secret redaction and prompt injection defense.
type SecurityGuardrail struct {
	secretPatterns    []*regexp.Regexp
	injectionPatterns []*regexp.Regexp
}

// NewSecurityGuardrail constructs a new SecurityGuardrail instance.
func NewSecurityGuardrail() *SecurityGuardrail {
	secretRegexes := []*regexp.Regexp{
		regexp.MustCompile(`sk-(ant-)?[a-zA-Z0-9_\-]{20,}`),
		regexp.MustCompile(`gh[pousr]_[a-zA-Z0-9]{20,}`),
		regexp.MustCompile(`github_pat_[a-zA-Z0-9_]{20,}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9_\.\-]{20,}`),
		regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)\s*[:=]\s*["']?([a-zA-Z0-9_\.\-]{12,})["']?`),
	}

	injectionRegexes := []*regexp.Regexp{
		regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions`),
		regexp.MustCompile(`(?i)disregard\s+(all\s+)?previous\s+(instructions|rules)`),
		regexp.MustCompile(`(?i)forget\s+(all\s+)?(previous\s+)?(instructions|rules)`),
		regexp.MustCompile(`(?i)you\s+are\s+now\s+(in\s+)?(dan|jailbreak)\s+mode`),
		regexp.MustCompile(`(?i)</(SYSTEM\s+RUNTIME\s+FOUNDATION|LONG_TERM_MEMORY|CORE_RULES)>`),
	}

	return &SecurityGuardrail{
		secretPatterns:    secretRegexes,
		injectionPatterns: injectionRegexes,
	}
}

// RedactSecrets scans text with comprehensive regex patterns and replaces secrets with [REDACTED].
func (s *SecurityGuardrail) RedactSecrets(text string) string {
	res := text
	for _, p := range s.secretPatterns {
		res = p.ReplaceAllStringFunc(res, func(match string) string {
			if strings.HasPrefix(strings.ToLower(match), "bearer ") {
				return "Bearer [REDACTED]"
			}
			if strings.Contains(match, ":") || strings.Contains(match, "=") {
				parts := strings.FieldsFunc(match, func(r rune) bool { return r == ':' || r == '=' })
				if len(parts) >= 2 {
					delim := ":"
					if strings.Contains(match, "=") {
						delim = "="
					}
					return fmtKeyValRedacted(parts[0], delim)
				}
			}
			return "[REDACTED]"
		})
	}
	return res
}

func fmtKeyValRedacted(key, delim string) string {
	return strings.TrimSpace(key) + delim + " [REDACTED]"
}

// ValidateInjection verifies that text contains no system-overriding prompt injections.
func (s *SecurityGuardrail) ValidateInjection(text string) error {
	for _, p := range s.injectionPatterns {
		if p.MatchString(text) {
			return ErrPromptInjectionDetected
		}
	}
	return nil
}
