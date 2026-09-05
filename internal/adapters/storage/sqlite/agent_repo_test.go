package sqlite_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteStore_AgentOwnershipAndRBAC(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	t.Run("Create and Retrieve Agent with Ownership and Public Flag", func(t *testing.T) {
		agent := &domain.Agent{
			Name:          "architect_bot",
			Description:   "System Architect",
			Status:        domain.StatusInitialized,
			WorkspacePath: "/data/architect",
			OwnerID:       "1001",
			IsPublic:      false,
		}
		require.NoError(t, store.SaveAgent(ctx, agent))

		retrieved, err := store.GetAgent(ctx, "architect_bot")
		require.NoError(t, err)
		assert.Equal(t, "architect_bot", retrieved.Name)
		assert.Equal(t, "1001", retrieved.OwnerID)
		assert.False(t, retrieved.IsPublic)
		assert.Equal(t, domain.PresetBalanced, retrieved.SecurityPreset, "Unspecified preset must default to balanced")

		// Update to Strict
		retrieved.SecurityPreset = domain.PresetStrict
		require.NoError(t, store.SaveAgent(ctx, retrieved))
		updated, err := store.GetAgent(ctx, "architect_bot")
		require.NoError(t, err)
		assert.Equal(t, domain.PresetStrict, updated.SecurityPreset, "Updated preset must persist accurately")
	})

	t.Run("ShareAgent and ListAgentPermissions", func(t *testing.T) {
		perm1 := &domain.AgentPermission{
			AgentName: "architect_bot",
			UserID:    "2002",
			Role:      "operator",
			GrantedBy: "1001",
			GrantedAt: time.Now(),
		}
		require.NoError(t, store.ShareAgent(ctx, perm1))

		perm2 := &domain.AgentPermission{
			AgentName: "architect_bot",
			UserID:    "3003",
			Role:      "viewer",
			GrantedBy: "1001",
			GrantedAt: time.Now(),
		}
		require.NoError(t, store.ShareAgent(ctx, perm2))

		perms, err := store.ListAgentPermissions(ctx, "architect_bot")
		require.NoError(t, err)
		assert.Len(t, perms, 2)
		assert.Equal(t, "2002", perms[0].UserID)
		assert.Equal(t, "operator", perms[0].Role)
		assert.Equal(t, "3003", perms[1].UserID)
		assert.Equal(t, "viewer", perms[1].Role)
	})

	t.Run("CheckAgentAccess Hierarchy", func(t *testing.T) {
		// Owner check
		allowed, role, err := store.CheckAgentAccess(ctx, "architect_bot", "1001")
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, "owner", role)

		// Shared operator check
		allowed, role, err = store.CheckAgentAccess(ctx, "architect_bot", "2002")
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, "operator", role)

		// Shared viewer check
		allowed, role, err = store.CheckAgentAccess(ctx, "architect_bot", "3003")
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, "viewer", role)

		// Unauthorized user check
		allowed, role, err = store.CheckAgentAccess(ctx, "architect_bot", "9999")
		require.NoError(t, err)
		assert.False(t, allowed)
		assert.Empty(t, role)

		// Public agent check
		publicAgent := &domain.Agent{
			Name:          "public_helper",
			Description:   "Public Assistant",
			Status:        domain.StatusInitialized,
			WorkspacePath: "/data/public",
			OwnerID:       "5005",
			IsPublic:      true,
		}
		require.NoError(t, store.SaveAgent(ctx, publicAgent))

		allowed, role, err = store.CheckAgentAccess(ctx, "public_helper", "9999")
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, "public", role)
	})

	t.Run("RevokeAgentAccess", func(t *testing.T) {
		require.NoError(t, store.RevokeAgentAccess(ctx, "architect_bot", "3003"))

		allowed, role, err := store.CheckAgentAccess(ctx, "architect_bot", "3003")
		require.NoError(t, err)
		assert.False(t, allowed)
		assert.Empty(t, role)
	})

	t.Run("ListAgentsForUser Filtering", func(t *testing.T) {
		// User 1001 owns "architect_bot", sees "public_helper"
		agents1001, err := store.ListAgentsForUser(ctx, "1001")
		require.NoError(t, err)
		names1001 := make([]string, len(agents1001))
		for i, a := range agents1001 {
			names1001[i] = a.Name
		}
		assert.Contains(t, names1001, "architect_bot")
		assert.Contains(t, names1001, "public_helper")

		// User 2002 is operator on "architect_bot", sees "public_helper"
		agents2002, err := store.ListAgentsForUser(ctx, "2002")
		require.NoError(t, err)
		names2002 := make([]string, len(agents2002))
		for i, a := range agents2002 {
			names2002[i] = a.Name
		}
		assert.Contains(t, names2002, "architect_bot")
		assert.Contains(t, names2002, "public_helper")

		// User 9999 has no grants, sees only "public_helper"
		agents9999, err := store.ListAgentsForUser(ctx, "9999")
		require.NoError(t, err)
		names9999 := make([]string, len(agents9999))
		for i, a := range agents9999 {
			names9999[i] = a.Name
		}
		assert.NotContains(t, names9999, "architect_bot")
		assert.Contains(t, names9999, "public_helper")
	})

	t.Run("Cascading Delete Permissions on Agent Deletion", func(t *testing.T) {
		// Create disposable agent with permission
		disposable := &domain.Agent{
			Name:          "disposable_agent",
			Status:        domain.StatusInitialized,
			WorkspacePath: "/data/disp",
			OwnerID:       "1001",
		}
		require.NoError(t, store.SaveAgent(ctx, disposable))
		require.NoError(t, store.ShareAgent(ctx, &domain.AgentPermission{
			AgentName: "disposable_agent",
			UserID:    "2002",
			Role:      "operator",
		}))

		perms, err := store.ListAgentPermissions(ctx, "disposable_agent")
		require.NoError(t, err)
		assert.Len(t, perms, 1)

		require.NoError(t, store.DeleteAgent(ctx, "disposable_agent"))

		permsAfter, err := store.ListAgentPermissions(ctx, "disposable_agent")
		require.NoError(t, err)
		assert.Len(t, permsAfter, 0)
	})

	t.Run("ClaimAgent Atomic Transition", func(t *testing.T) {
		// 1. Create an unowned, private agent
		unowned := &domain.Agent{
			Name:          "unowned_agent",
			Status:        domain.StatusInitialized,
			WorkspacePath: "/data/unowned",
			OwnerID:       "",
			IsPublic:      false,
		}
		require.NoError(t, store.SaveAgent(ctx, unowned))

		// Access check for any user must be false (not public, not owner)
		allowed, _, err := store.CheckAgentAccess(ctx, "unowned_agent", "1001")
		require.NoError(t, err)
		assert.False(t, allowed, "Unowned private agent must not allow access to arbitrary users")

		// 2. Claim agent
		claimed, err := store.ClaimAgent(ctx, "unowned_agent", "1001")
		require.NoError(t, err)
		assert.True(t, claimed, "Claiming unowned agent should succeed")

		// Verify user 1001 is now owner
		retrieved, err := store.GetAgent(ctx, "unowned_agent")
		require.NoError(t, err)
		assert.Equal(t, "1001", retrieved.OwnerID)

		// Access check now allows user 1001 as owner
		allowed, role, err := store.CheckAgentAccess(ctx, "unowned_agent", "1001")
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, "owner", role)

		// 3. Attempting to claim already-owned agent must fail (rowsAffected == 0)
		claimedAgain, err := store.ClaimAgent(ctx, "unowned_agent", "2002")
		require.NoError(t, err)
		assert.False(t, claimedAgain, "Claiming already owned agent must return false")

		// 4. Attempting to claim non-existent agent returns false
		claimedNonExistent, err := store.ClaimAgent(ctx, "non_existent_agent", "1001")
		require.NoError(t, err)
		assert.False(t, claimedNonExistent, "Claiming non-existent agent must return false")
	})
}

