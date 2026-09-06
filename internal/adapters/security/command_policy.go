package security

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"agyent/internal/core/domain"
)

// ParsedCommand captures an individual executable invocation within a shell pipeline or compound command.
type ParsedCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Raw        string   `json:"raw"`
}

// ParseCommandPipeline breaks a compound shell command string into its constituent discrete command invocations.
// It handles pipelines (|), conditional operators (&&, ||), sequence delimiters (;), subshells, and interpreter flags (-c, -e, /c).
func ParseCommandPipeline(rawCmd string) []ParsedCommand {
	trimmed := strings.TrimSpace(rawCmd)
	if trimmed == "" {
		return nil
	}

	// Split by top-level operators while respecting quotes
	segments := splitPipelineSegments(trimmed)
	var commands []ParsedCommand

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}

		tokens := tokenizeCommandArgs(seg)
		if len(tokens) == 0 {
			continue
		}

		// Extract base executable
		execIdx := 0
		// Skip leading env vars (e.g., VAR=val cmd)
		for execIdx < len(tokens) && strings.Contains(tokens[execIdx], "=") &&
			!strings.HasPrefix(tokens[execIdx], "./") && !strings.HasPrefix(tokens[execIdx], "/") && !strings.HasPrefix(tokens[execIdx], `\`) {
			execIdx++
		}
		if execIdx >= len(tokens) {
			execIdx = 0
		}

		rawExec := tokens[execIdx]
		baseExec := filepath.Base(rawExec)
		baseExec = strings.TrimSuffix(baseExec, ".exe")

		args := tokens[execIdx+1:]
		commands = append(commands, ParsedCommand{
			Executable: strings.ToLower(baseExec),
			Args:       args,
			Raw:        seg,
		})

		// Check for interpreter invocation executing nested commands (e.g., bash -c "...", python3 -c "...", cmd /c "...")
		nestedScript := extractNestedInterpreterCommand(baseExec, args)
		if nestedScript != "" {
			nestedCmds := ParseCommandPipeline(nestedScript)
			commands = append(commands, nestedCmds...)
		}

		// Check for command substitutions $(...) and `...`
		for _, sub := range extractCommandSubstitutions(seg) {
			subCmds := ParseCommandPipeline(sub)
			commands = append(commands, subCmds...)
		}
	}

	return commands
}

func extractCommandSubstitutions(input string) []string {
	var subs []string
	chars := []rune(input)
	n := len(chars)
	for i := 0; i < n; i++ {
		if chars[i] == '$' && i+1 < n && chars[i+1] == '(' {
			start := i + 2
			depth := 1
			j := start
			inSingleQuote := false
			inDoubleQuote := false
			escaped := false

			for j < n && depth > 0 {
				r := chars[j]
				if escaped {
					escaped = false
					j++
					continue
				}
				if r == '\\' && !inSingleQuote {
					escaped = true
					j++
					continue
				}
				if r == '\'' && !inDoubleQuote {
					inSingleQuote = !inSingleQuote
					j++
					continue
				}
				if r == '"' && !inSingleQuote {
					inDoubleQuote = !inDoubleQuote
					j++
					continue
				}
				if !inSingleQuote && !inDoubleQuote {
					if r == '(' {
						depth++
					} else if r == ')' {
						depth--
					}
				}
				j++
			}
			if depth == 0 {
				subs = append(subs, string(chars[start:j-1]))
				i = j - 1
			}
		} else if chars[i] == '`' {
			start := i + 1
			j := start
			for j < n && chars[j] != '`' {
				j++
			}
			if j < n {
				subs = append(subs, string(chars[start:j]))
				i = j
			}
		}
	}
	return subs
}

func extractNestedInterpreterCommand(baseExec string, args []string) string {
	lower := strings.ToLower(baseExec)
	switch lower {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i, a := range args {
			if isShellCommandFlag(a) && i+1 < len(args) {
				return args[i+1]
			}
		}
	case "python", "python3", "py":
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				return args[i+1]
			}
		}
	case "node", "nodejs", "perl", "ruby":
		for i, a := range args {
			if (a == "-e" || a == "-c") && i+1 < len(args) {
				return args[i+1]
			}
		}
	case "powershell", "pwsh":
		for i, a := range args {
			if (strings.EqualFold(a, "-Command") || strings.EqualFold(a, "-c") || strings.EqualFold(a, "/c")) && i+1 < len(args) {
				return args[i+1]
			}
		}
	case "cmd":
		for i, a := range args {
			if (strings.EqualFold(a, "/c") || strings.EqualFold(a, "/k")) && i+1 < len(args) {
				return strings.Join(args[i+1:], " ")
			}
		}
	}
	return ""
}

func isShellCommandFlag(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	return strings.HasSuffix(arg, "c")
}

func splitPipelineSegments(input string) []string {
	var segments []string
	var current strings.Builder

	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	escaped := false

	chars := []rune(input)
	n := len(chars)

	for i := 0; i < n; i++ {
		r := chars[i]

		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingleQuote {
			escaped = true
			current.WriteRune(r)
			continue
		}

		if r == '\'' && !inDoubleQuote && !inBacktick {
			inSingleQuote = !inSingleQuote
			current.WriteRune(r)
			continue
		}
		if r == '"' && !inSingleQuote && !inBacktick {
			inDoubleQuote = !inDoubleQuote
			current.WriteRune(r)
			continue
		}
		if r == '`' && !inSingleQuote && !inDoubleQuote {
			inBacktick = !inBacktick
			current.WriteRune(r)
			continue
		}

		if !inSingleQuote && !inDoubleQuote && !inBacktick {
			// Check for command separators: &&, ||, |, ;, \n, &
			if r == ';' || r == '\n' {
				segments = append(segments, current.String())
				current.Reset()
				continue
			}
			if r == '|' {
				if i+1 < n && chars[i+1] == '|' {
					// ||
					segments = append(segments, current.String())
					current.Reset()
					i++
					continue
				}
				// |
				segments = append(segments, current.String())
				current.Reset()
				continue
			}
			if r == '&' {
				if i+1 < n && chars[i+1] == '&' {
					// &&
					segments = append(segments, current.String())
					current.Reset()
					i++
					continue
				}
				// Single & (background operator, excluding redirection like >& or &> or 2>&1)
				if i+1 < n && (chars[i+1] == '>' || chars[i+1] == '1' || chars[i+1] == '2') {
					// Redirection syntax
				} else {
					segments = append(segments, current.String())
					current.Reset()
					continue
				}
			}
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}

	return segments
}

func tokenizeCommandArgs(seg string) []string {
	var tokens []string
	var current strings.Builder

	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for _, r := range seg {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingleQuote {
			escaped = true
			continue
		}
		if r == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}
		if r == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if (r == ' ' || r == '\t' || r == '\r') && !inSingleQuote && !inDoubleQuote {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// EvaluateParsedCommandPolicy checks all constituent commands in a pipeline against security guardrails.
func EvaluateParsedCommandPolicy(
	parsedCmds []ParsedCommand,
	rawCmd string,
	preset domain.SecurityPreset,
	sensitiveRegex *regexp.Regexp,
	blacklist []*regexp.Regexp,
	whitelist []*regexp.Regexp,
) (domain.SecurityDecision, error) {
	if len(parsedCmds) == 0 {
		return domain.SecurityDecision{Decision: domain.DecisionDeny, Reason: "Empty command"}, nil
	}

	// 1. Check Anti-Self-Escalation across raw and all parsed commands
	if isSelfEscalationCommand(rawCmd) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Execution of administrative command '%s' is strictly forbidden", rawCmd),
		}, nil
	}

	for _, cmd := range parsedCmds {
		if isSelfEscalationCommand(cmd.Executable) || isSelfEscalationCommand(cmd.Raw) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Subcommand '%s' references forbidden gateway resources", cmd.Raw),
			}, nil
		}
	}

	// 2. Check Blacklist Patterns
	for _, bl := range blacklist {
		if bl.MatchString(rawCmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Command matches forbidden pattern '%s'", bl.String()),
			}, nil
		}
		for _, cmd := range parsedCmds {
			if bl.MatchString(cmd.Raw) || bl.MatchString(cmd.Executable) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Pipeline subcommand '%s' matches forbidden pattern '%s'", cmd.Raw, bl.String()),
				}, nil
			}
		}
	}

	// 2.5. Deep-scan command arguments and redirections for control-plane and sensitive paths
	for _, cmd := range parsedCmds {
		if hasControlPlanePathReference(cmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Control Plane Protection]: Command '%s' attempts to reference or mutate forbidden control-plane or system paths", cmd.Raw),
			}, nil
		}
	}

	// 2.6. Check Process/Host Enumeration in Workspace-Only preset
	if preset == domain.PresetWorkspaceOnly {
		for _, cmd := range parsedCmds {
			if isHostProcessEnumerationCommand(cmd.Executable) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Preset: Workspace Only]: Host process or system resource inspection '%s' is strictly forbidden outside workspace scope", cmd.Executable),
				}, nil
			}
		}
	}

	// 3. Check Whitelist in Strict preset
	if preset == domain.PresetStrict {
		for _, cmd := range parsedCmds {
			isWhitelisted := false
			for _, wl := range whitelist {
				if wl.MatchString(cmd.Executable) || wl.MatchString(cmd.Raw) {
					isWhitelisted = true
					break
				}
			}
			if !isWhitelisted {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Preset: Strict]: Pipeline executable '%s' is not explicitly whitelisted", cmd.Executable),
				}, nil
			}
		}
	}

	// 4. Check Sensitive Commands in Balanced preset -> Trigger HITL
	if preset == domain.PresetBalanced {
		for _, cmd := range parsedCmds {
			if sensitiveRegex.MatchString(cmd.Executable) || sensitiveRegex.MatchString(cmd.Raw) {
				return domain.SecurityDecision{
					Decision: domain.DecisionAsk,
					Reason:   fmt.Sprintf("Sensitive shell execution: `%s`", cmd.Raw),
				}, nil
			}
		}
	}

	return domain.SecurityDecision{
		Decision: domain.DecisionAllow,
		Reason:   "Command permitted under active security profile",
	}, nil
}

func isHostProcessEnumerationCommand(execName string) bool {
	lower := strings.ToLower(execName)
	switch lower {
	case "ps", "top", "htop", "pm2", "pgrep", "pkill", "kill", "lsof", "netstat", "ss",
		"who", "w", "last", "id", "env", "printenv", "set", "systemctl", "service",
		"launchctl", "journalctl", "dmesg", "uname", "hostname", "whoami":
		return true
	}
	return false
}

var controlPlaneRegexPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[/\\])\.agents[/\\]hooks\.json`),
	regexp.MustCompile(`(?i)(^|[/\\])\.agyent[/\\](config\.ya?ml|config\.json|agyent\.db([.-].*)?$)`),
	regexp.MustCompile(`(?i)\bagyent\.db([.-].*)?\b`),
	regexp.MustCompile(`(?i)(^|[/\\])\.gemini[/\\]config`),
	regexp.MustCompile(`(?i)(/private)?/etc/(shadow|passwd|sudoers)`),
	regexp.MustCompile(`(?i)(^|[/\\])\.(ssh|aws|kube|gnupg)([/\\]|$)`),
}

func hasControlPlanePathReference(cmd ParsedCommand) bool {
	// Check raw command line
	for _, p := range controlPlaneRegexPatterns {
		if p.MatchString(cmd.Raw) {
			return true
		}
	}

	// Check all token arguments
	for _, arg := range cmd.Args {
		for _, p := range controlPlaneRegexPatterns {
			if p.MatchString(arg) {
				return true
			}
		}
	}

	return false
}

// ExtractScriptFileReferences extracts referenced local script filenames from command invocations
// (e.g., python3 foo.py, bash ./run.sh, node app.js, perl script.pl, ./myscript.sh).
func ExtractScriptFileReferences(parsedCmds []ParsedCommand) []string {
	var scripts []string
	for _, pcmd := range parsedCmds {
		execLower := strings.ToLower(pcmd.Executable)
		switch execLower {
		case "python", "python3", "py", "sh", "bash", "zsh", "dash", "ksh", "node", "nodejs", "ts-node", "deno", "bun", "perl", "ruby", "php":
			for _, arg := range pcmd.Args {
				if strings.HasPrefix(arg, "-") {
					continue
				}
				// Potential script file path
				if strings.HasSuffix(arg, ".py") || strings.HasSuffix(arg, ".sh") || strings.HasSuffix(arg, ".bash") ||
					strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".ts") || strings.HasSuffix(arg, ".mjs") ||
					strings.HasSuffix(arg, ".cjs") || strings.HasSuffix(arg, ".pl") || strings.HasSuffix(arg, ".rb") ||
					strings.HasSuffix(arg, ".php") || strings.Contains(arg, "/") || strings.Contains(arg, `\`) {
					scripts = append(scripts, arg)
				}
			}
		default:
			// Check direct script invocation like ./script.sh or ./foo.py or VAR=1 ./script.sh
			tokens := tokenizeCommandArgs(pcmd.Raw)
			for _, tok := range tokens {
				if strings.Contains(tok, "=") && !strings.HasPrefix(tok, "./") && !strings.HasPrefix(tok, "/") && !strings.HasPrefix(tok, `\`) {
					continue
				}
				if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, ".\\") || strings.HasPrefix(tok, "/") ||
					strings.HasSuffix(strings.ToLower(tok), ".sh") || strings.HasSuffix(strings.ToLower(tok), ".py") ||
					strings.HasSuffix(strings.ToLower(tok), ".js") || strings.HasSuffix(strings.ToLower(tok), ".ts") {
					scripts = append(scripts, tok)
				}
				break
			}
		}
	}
	return scripts
}
