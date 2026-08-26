package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestSQLiteStore_ConversationRepository(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_conv.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	// 1. Prepare agent and session
	agent := &domain.Agent{
		Name:          "dev_agent",
		Description:   "Developer Agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: tempDir,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("failed to save agent: %v", err)
	}

	sessionKey := "telegram:12345"
	session, err := store.GetOrCreateSession(ctx, sessionKey, agent.Name)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 2. Test TouchConversation (New Conversation)
	convID1 := "f8b6c9bf-55ad-405a-90d8-7a96305a1221"
	prompt1 := "Hãy viết cho anh một web server Go"
	if err := store.TouchConversation(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, convID1, prompt1); err != nil {
		t.Fatalf("failed to touch conversation 1: %v", err)
	}

	conv1, err := store.GetConversation(ctx, convID1)
	if err != nil {
		t.Fatalf("failed to get conversation 1: %v", err)
	}
	if conv1.ID != convID1 {
		t.Errorf("expected ID %s, got %s", convID1, conv1.ID)
	}
	if conv1.Title != "Hãy viết cho anh một web server Go" {
		t.Errorf("unexpected auto-title: %s", conv1.Title)
	}
	if conv1.TurnCount != 1 {
		t.Errorf("expected turn count 1, got %d", conv1.TurnCount)
	}

	// 3. Test TouchConversation (Existing Conversation Increment)
	if err := store.TouchConversation(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, convID1, "Cảm ơn em"); err != nil {
		t.Fatalf("failed to touch conversation 1 second time: %v", err)
	}
	conv1Updated, _ := store.GetConversation(ctx, convID1)
	if conv1Updated.TurnCount != 2 {
		t.Errorf("expected turn count 2, got %d", conv1Updated.TurnCount)
	}

	// 4. Test TouchConversation for Second Conversation
	convID2 := "f8b6c9bf-55ad-405a-90d8-7a96305a1222"
	prompt2 := "Debug lỗi websocket"
	time.Sleep(10 * time.Millisecond)
	if err := store.TouchConversation(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, convID2, prompt2); err != nil {
		t.Fatalf("failed to touch conversation 2: %v", err)
	}

	// 5. Test ListRecentConversations
	list, total, err := store.ListRecentConversations(ctx, sessionKey, agent.Name, "", 10, 0)
	if err != nil {
		t.Fatalf("failed to list conversations: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("expected 2 active conversations, got total=%d len=%d", total, len(list))
	}
	// Most recently touched should be first (convID2)
	if list[0].ID != convID2 {
		t.Errorf("expected top conversation to be %s, got %s", convID2, list[0].ID)
	}
	if list[0].AliasIndex != 1 {
		t.Errorf("expected alias index 1, got %d", list[0].AliasIndex)
	}

	// 6. Test GetConversationByAlias
	byAlias1, err := store.GetConversationByAlias(ctx, sessionKey, agent.Name, "", 1)
	if err != nil {
		t.Fatalf("failed to get conversation by alias 1: %v", err)
	}
	if byAlias1.ID != convID2 {
		t.Errorf("expected alias 1 to be %s, got %s", convID2, byAlias1.ID)
	}

	byAlias2, err := store.GetConversationByAlias(ctx, sessionKey, agent.Name, "", 2)
	if err != nil {
		t.Fatalf("failed to get conversation by alias 2: %v", err)
	}
	if byAlias2.ID != convID1 {
		t.Errorf("expected alias 2 to be %s, got %s", convID1, byAlias2.ID)
	}

	// 7. Test SetConversationPinned
	if err := store.SetConversationPinned(ctx, convID1, true); err != nil {
		t.Fatalf("failed to pin conversation 1: %v", err)
	}
	// After pinning conv1, it should become top of the list!
	listAfterPin, _, _ := store.ListRecentConversations(ctx, sessionKey, agent.Name, "", 10, 0)
	if listAfterPin[0].ID != convID1 {
		t.Errorf("expected pinned conversation %s to be first, got %s", convID1, listAfterPin[0].ID)
	}
	if !listAfterPin[0].IsPinned {
		t.Errorf("expected IsPinned=true")
	}

	// 8. Test SetConversationTitle
	newTitle := "Web Server Go & Dockerfile"
	if err := store.SetConversationTitle(ctx, convID1, newTitle); err != nil {
		t.Fatalf("failed to set title: %v", err)
	}
	conv1Renamed, _ := store.GetConversation(ctx, convID1)
	if conv1Renamed.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, conv1Renamed.Title)
	}

	// 9. Test SetConversationArchived
	if err := store.SetConversationArchived(ctx, convID2, true); err != nil {
		t.Fatalf("failed to archive conversation 2: %v", err)
	}
	listAfterArchive, totalAfterArchive, _ := store.ListRecentConversations(ctx, sessionKey, agent.Name, "", 10, 0)
	if totalAfterArchive != 1 || len(listAfterArchive) != 1 {
		t.Errorf("expected 1 non-archived conversation, got total=%d len=%d", totalAfterArchive, len(listAfterArchive))
	}

	// 10. Test GetExpiredArchivedConversationIDs and PurgeConversations
	// convID2 is archived and not pinned
	expiredIDs, err := store.GetExpiredArchivedConversationIDs(ctx, 0) // 0 days cutoff for testing
	if err != nil {
		t.Fatalf("failed to get expired IDs: %v", err)
	}
	if len(expiredIDs) != 1 || expiredIDs[0] != convID2 {
		t.Errorf("expected expired ID %s, got %v", convID2, expiredIDs)
	}

	if err := store.PurgeConversations(ctx, expiredIDs); err != nil {
		t.Fatalf("failed to purge conversations: %v", err)
	}

	// Verify convID2 is deleted
	_, err = store.GetConversation(ctx, convID2)
	if err == nil {
		t.Errorf("expected error getting purged conversation, got nil")
	}
}
