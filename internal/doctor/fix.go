package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	securityAdapter "agyent/internal/adapters/security"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// ApplyFixes attempts to automatically repair issues flagged with CanAutoFix = true.
func (d *DoctorRunner) ApplyFixes(ctx context.Context, report *DiagnosticReport) {
	if d.cfg == nil {
		return
	}

	for i := range report.Results {
		res := &report.Results[i]
		if !res.CanAutoFix || res.Status == StatusPass {
			continue
		}

		switch res.Category {
		case CategoryStorage:
			if err := d.fixStorage(ctx); err == nil {
				res.Status = StatusPass
				res.Fixed = true
				res.Message += " (Auto-fixed: directories & database initialized)"
			}

		case CategorySecurity:
			if err := d.fixSecurityHooks(); err == nil {
				res.Status = StatusPass
				res.Fixed = true
				res.Message += " (Auto-fixed: security hooks provisioned)"
			}

		case CategoryConfig:
			if res.Name == "Configuration File Existence" {
				if err := config.Save(d.opts.ConfigPath, d.cfg); err == nil {
					res.Status = StatusPass
					res.Fixed = true
					res.Message = fmt.Sprintf("Configuration file created with defaults at %s", d.opts.ConfigPath)
				}
			}
		}
	}
}

func (d *DoctorRunner) fixStorage(ctx context.Context) error {
	// 1. Create Agents Dir
	if d.cfg.Storage.AgentsDir != "" {
		_ = os.MkdirAll(d.cfg.Storage.AgentsDir, 0755)
	}

	// 2. Create SQLite DB Directory and initialize DB
	expandedDBPath, err := config.ExpandPath(d.cfg.Storage.DBPath)
	if err == nil {
		_ = os.MkdirAll(filepath.Dir(expandedDBPath), 0755)
		store, err := sqlite.Open(expandedDBPath)
		if err == nil {
			defer store.Close()

			// Seed admins
			for _, adminID := range d.cfg.Telegram.AdminUserIDs {
				_ = store.SaveUser(ctx, &domain.User{
					ID:        strconv.FormatInt(adminID, 10),
					Role:      "admin",
					CreatedAt: time.Now(),
				})
			}

			// Seed default agent if not exists
			starterWS := config.ResolveAgentWorkspace(d.cfg.Storage.AgentsDir, "agyent")
			_ = os.MkdirAll(starterWS, 0755)
			_ = store.SaveAgent(ctx, &domain.Agent{
				Name:          "agyent",
				Description:   "Agyent - Autonomous All-in-One Personal AI Assistant & Pair Programmer",
				Status:        domain.StatusUninitialized,
				WorkspacePath: starterWS,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			})
		}
	}

	return nil
}

func (d *DoctorRunner) fixSecurityHooks() error {
	if d.cfg.Storage.AgentsDir == "" {
		return nil
	}
	starterWS := config.ResolveAgentWorkspace(d.cfg.Storage.AgentsDir, "")
	_, err := securityAdapter.EnsureWorkspaceHooksProvisioned(starterWS, "", nil)
	return err
}
