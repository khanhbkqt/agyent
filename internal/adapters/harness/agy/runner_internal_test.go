package agy

import (
	"strings"
	"testing"

	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
)

func TestBuildCommandEnv_WindowsUserProfile(t *testing.T) {
	req := domain.ExecutionRequest{
		AgentName:    "win-agent",
		WorkspaceDir: t.TempDir(),
		TurnID:       "turn-win-01",
	}

	t.Setenv("USERPROFILE", `C:\Users\testuser`)
	t.Setenv("HOMEDRIVE", `C:`)
	t.Setenv("HOMEPATH", `\Users\testuser`)
	t.Setenv("PROGRAMDATA", `C:\ProgramData`)

	env := buildCommandEnv(req)
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[strings.ToUpper(parts[0])] = parts[1]
		}
	}

	assert.Equal(t, `C:\Users\testuser`, envMap["USERPROFILE"])
	assert.Equal(t, `C:`, envMap["HOMEDRIVE"])
	assert.Equal(t, `\Users\testuser`, envMap["HOMEPATH"])
	assert.Equal(t, `C:\ProgramData`, envMap["PROGRAMDATA"])
	assert.Equal(t, "turn-win-01", envMap["AGYENT_TURN_ID"])
}
