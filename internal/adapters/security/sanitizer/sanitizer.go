package sanitizer

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

var (
	// Known high-risk secret patterns
	openAIRegex       = regexp.MustCompile(`(?i)\b(sk-[a-zA-Z0-9_\-]{20,})\b`)
	anthropicRegex    = regexp.MustCompile(`(?i)\b(sk-ant-[a-zA-Z0-9_\-]{20,})\b`)
	githubPATRegex    = regexp.MustCompile(`\b(gh[pousr]_[a-zA-Z0-9]{20,})\b`)
	githubFineRegex   = regexp.MustCompile(`\b(github_pat_[a-zA-Z0-9_]{82})\b`)
	geminiAIRegex     = regexp.MustCompile(`\b(AIzaSy[a-zA-Z0-9_\-]{33})\b`)
	awsAKIRegex       = regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)
	awsSTSRegex       = regexp.MustCompile(`\b(ASIA[0-9A-Z]{16})\b`)
	bearerAuthRegex   = regexp.MustCompile(`(?i)\b(Bearer\s+[a-zA-Z0-9_\-\.]{20,})\b`)
	privateKeyRegex   = regexp.MustCompile(`-----BEGIN [A-Z0-9_-]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9_-]+ PRIVATE KEY-----`)
	genericKVRegex    = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_\-]*(?:password|secret|api_key|token|access_key)[a-zA-Z0-9_\-]*)\s*([:=])\s*(["']?)([^\s"'\r\n]{6,})(["']?)`)
	promptInjRegex    = regexp.MustCompile(`(?i)(ignore\s+all\s+previous\s+instructions|system\s+override|you\s+are\s+now\s+an\s+unrestricted)`)
)

// Evaluator provides secret masking and indirect prompt injection defense.
type Evaluator struct {
	mu                  sync.RWMutex
	enabled             bool
	redactionMode       domain.RedactionMode
	sanitizeToolOutputs bool
	whitelistedEnvKeys  map[string]bool
}

// NewEvaluator constructs a sanitizer evaluator.
func NewEvaluator(cfg config.DLPConfig) *Evaluator {
	mode := domain.RedactionMode(cfg.RedactionMode)
	if mode == "" {
		mode = domain.RedactStrict
	}

	whitelisted := make(map[string]bool)
	for _, key := range cfg.WhitelistedEnvKeys {
		whitelisted[strings.ToUpper(strings.TrimSpace(key))] = true
	}

	return &Evaluator{
		enabled:             cfg.Enabled,
		redactionMode:       mode,
		sanitizeToolOutputs: cfg.SanitizeToolOutputs,
		whitelistedEnvKeys:  whitelisted,
	}
}

// SetRedactionMode dynamically updates the redaction posture.
func (e *Evaluator) SetRedactionMode(mode domain.RedactionMode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.redactionMode = mode
}

// RedactSecrets masks detected API keys and secrets within text.
func (e *Evaluator) RedactSecrets(text string) string {
	if text == "" {
		return text
	}

	e.mu.RLock()
	enabled := e.enabled
	mode := e.redactionMode
	whitelisted := e.whitelistedEnvKeys
	e.mu.RUnlock()

	if !enabled || mode == domain.RedactAuditOnly {
		return text
	}

	res := text

	// 1. Mask well-known API Tokens
	res = openAIRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = anthropicRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = githubPATRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = githubFineRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = geminiAIRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = awsAKIRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = awsSTSRegex.ReplaceAllString(res, "[REDACTED_SECRET]")
	res = bearerAuthRegex.ReplaceAllString(res, "Bearer [REDACTED_SECRET]")
	res = privateKeyRegex.ReplaceAllString(res, "[REDACTED_PRIVATE_KEY]")

	// 2. Mask Generic Key-Value assignments preserving syntax (JSON/YAML/env)
	res = genericKVRegex.ReplaceAllStringFunc(res, func(match string) string {
		sub := genericKVRegex.FindStringSubmatch(match)
		if len(sub) >= 6 {
			key := sub[1]
			sep := sub[2]
			quoteOpen := sub[3]
			quoteClose := sub[5]

			cleanKey := strings.Trim(key, "\"'")
			if mode == domain.RedactPermissive && whitelisted[strings.ToUpper(cleanKey)] {
				return match
			}

			if sep == ":" {
				return fmt.Sprintf("%s: %s[REDACTED_SECRET]%s", key, quoteOpen, quoteClose)
			}
			return fmt.Sprintf("%s=%s[REDACTED_SECRET]%s", key, quoteOpen, quoteClose)
		}
		return match
	})

	return res
}

// ValidateInjection scans text for obvious prompt injection triggers.
func (e *Evaluator) ValidateInjection(text string) error {
	if promptInjRegex.MatchString(text) {
		return fmt.Errorf("🛡️ [Security Gateway]: Potential prompt injection detected in tool output")
	}
	return nil
}
