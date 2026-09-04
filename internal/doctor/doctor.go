package doctor

import (
	"context"
	"time"

	"agyent/internal/config"
)

// CheckStatus represents the severity outcome of a diagnostic check.
type CheckStatus string

const (
	StatusPass CheckStatus = "PASS"
	StatusWarn CheckStatus = "WARN"
	StatusFail CheckStatus = "FAIL"
	StatusInfo CheckStatus = "INFO"
)

// Category groups related diagnostic checks together.
type Category string

const (
	CategorySystem   Category = "System & Environment"
	CategoryConfig   Category = "Configuration"
	CategoryAGY      Category = "Antigravity (AGY) CLI & Quota"
	CategoryStorage  Category = "SQLite Database & Sessions"
	CategoryTelegram Category = "Telegram Gateway & Auth"
	CategoryZalo     Category = "Zalo Bot Gateway & Auth"
	CategorySecurity Category = "Security Gateway & Hooks"
	CategoryPlugins  Category = "Plugins & MCP Syncer"
)

// CheckResult holds the outcome of a single diagnostic check.
type CheckResult struct {
	Name        string      `json:"name"`
	Category    Category    `json:"category"`
	Status      CheckStatus `json:"status"`
	Message     string      `json:"message"`
	Details     string      `json:"details,omitempty"`
	Remediation string      `json:"remediation,omitempty"`
	CanAutoFix  bool        `json:"can_auto_fix,omitempty"`
	Fixed       bool        `json:"fixed,omitempty"`
}

// DiagnosticReport represents the aggregated results of all diagnostic checks.
type DiagnosticReport struct {
	Timestamp   time.Time     `json:"timestamp"`
	DurationMs  int64         `json:"duration_ms"`
	PassedCount int           `json:"passed_count"`
	WarnCount   int           `json:"warn_count"`
	FailCount   int           `json:"fail_count"`
	InfoCount   int           `json:"info_count"`
	Results     []CheckResult `json:"results"`
}

// SummaryStatus returns the overall health state based on failure and warning tallies.
func (r *DiagnosticReport) SummaryStatus() CheckStatus {
	if r.FailCount > 0 {
		return StatusFail
	}
	if r.WarnCount > 0 {
		return StatusWarn
	}
	return StatusPass
}

// Options configure the behavior of DoctorRunner.
type Options struct {
	ConfigPath    string
	TargetSession string
	SkipNetwork   bool
	LiveProbe     bool // If true, executes live lightweight AGY model/quota check
	AutoFix       bool
}

// DoctorRunner executes the diagnostic test battery.
type DoctorRunner struct {
	opts Options
	cfg  *config.Config
}

// NewRunner creates a new DoctorRunner instance with the given options.
func NewRunner(opts Options) *DoctorRunner {
	if opts.ConfigPath == "" {
		opts.ConfigPath = "~/.agyent/config.yaml"
	}
	return &DoctorRunner{
		opts: opts,
	}
}

// Run executes all diagnostic checks and compiles the final report.
func (d *DoctorRunner) Run(ctx context.Context) (*DiagnosticReport, error) {
	start := time.Now()
	report := &DiagnosticReport{
		Timestamp: start,
		Results:   make([]CheckResult, 0),
	}

	// 1. System & Host checks (including zombie process detection)
	report.Results = append(report.Results, d.CheckSystem()...)
	report.Results = append(report.Results, d.CheckZombies(ctx)...)

	// 2. Configuration checks
	cfgResults, loadedCfg := d.CheckConfiguration()
	report.Results = append(report.Results, cfgResults...)
	d.cfg = loadedCfg

	// If config failed to load, construct fallback config to allow subsequent checks to run
	if d.cfg == nil {
		d.cfg = config.DefaultConfig()
		_ = d.cfg.ExpandPaths()
	}

	// 3. Antigravity (AGY) CLI, Auth & Quota checks
	report.Results = append(report.Results, d.CheckAGY(ctx)...)

	// 4. SQLite Storage & Session checks
	report.Results = append(report.Results, d.CheckStorage(ctx)...)
	report.Results = append(report.Results, d.CheckStaleLocks()...)

	// 5. Telegram & Zalo Gateway & Live Auth checks
	if !d.opts.SkipNetwork {
		report.Results = append(report.Results, d.CheckTelegram(ctx)...)
		report.Results = append(report.Results, d.CheckZalo(ctx)...)
	} else {
		report.Results = append(report.Results, CheckResult{
			Name:     "Channel Bot Connectivity",
			Category: CategoryTelegram,
			Status:   StatusInfo,
			Message:  "Skipped live Bot API checks (--skip-network enabled)",
		})
	}

	// 6. Security Gateway & Hooks checks
	report.Results = append(report.Results, d.CheckSecurity()...)

	// 7. Plugins & MCP checks
	report.Results = append(report.Results, d.CheckPlugins()...)

	// 8. Auto-Fix execution if requested
	if d.opts.AutoFix {
		d.ApplyFixes(ctx, report)
	}

	// Calculate counts
	for _, res := range report.Results {
		switch res.Status {
		case StatusPass:
			report.PassedCount++
		case StatusWarn:
			report.WarnCount++
		case StatusFail:
			report.FailCount++
		case StatusInfo:
			report.InfoCount++
		}
	}

	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}
