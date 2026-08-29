package evolution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.EvolutionOrchestratorPort = (*EvolutionOrchestrator)(nil)

type evolutionTask struct {
	snapshot   domain.ConversationSnapshot
	trigger    domain.EvolutionTrigger
	retryCount int
}

// EvolutionOrchestrator coordinates the end-to-end asynchronous self-learning pipeline.
type EvolutionOrchestrator struct {
	cfg        *config.EvolutionConfig
	storage    ports.StoragePort
	taskGuard  ports.TaskStateGuardPort
	filter     ports.HeuristicFilterPort
	reflection ports.ReflectionEnginePort
	memory     ports.MemoryStorePort
	scanner    *IdleScanner
	agentsDir  string

	queue   chan evolutionTask
	running atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	genMap   sync.Map // sessionKey -> int64
	abortMap sync.Map // sessionKey -> chan struct{}
}

// NewEvolutionOrchestrator constructs and wires a new EvolutionOrchestrator instance.
func NewEvolutionOrchestrator(
	cfg *config.Config,
	storage ports.StoragePort,
	runner ports.RunnerPort,
) *EvolutionOrchestrator {
	var evoCfg config.EvolutionConfig
	var agentsDir string
	if cfg != nil {
		evoCfg = cfg.Evolution
		agentsDir = cfg.Storage.AgentsDir
	} else {
		evoCfg = config.EvolutionConfig{
			Enabled:                  true,
			IdleTimeoutMinutes:       15,
			ScanIntervalMinutes:      5,
			ConfidenceThreshold:      0.85,
			ReflectionTimeoutSeconds: 30,
			CompactionLineLimit:      200,
			QueueCapacity:            100,
		}
	}

	if evoCfg.QueueCapacity <= 0 {
		evoCfg.QueueCapacity = 100
	}

	guardrail := NewSecurityGuardrail()
	taskGuard := NewTaskStateGuard()
	heuristicFilter := NewHeuristicFilter(evoCfg.ConfidenceThreshold)
	reflectionEngine := NewReflectionEngine(runner, guardrail, time.Duration(evoCfg.ReflectionTimeoutSeconds)*time.Second)
	conflictResolver := NewConflictResolver()
	memoryStore := NewMemoryStore(storage, conflictResolver)

	ctx, cancel := context.WithCancel(context.Background())

	orch := &EvolutionOrchestrator{
		cfg:        &evoCfg,
		storage:    storage,
		taskGuard:  taskGuard,
		filter:     heuristicFilter,
		reflection: reflectionEngine,
		memory:     memoryStore,
		agentsDir:  agentsDir,
		queue:      make(chan evolutionTask, evoCfg.QueueCapacity),
		ctx:        ctx,
		cancel:     cancel,
	}

	orch.scanner = NewIdleScanner(storage, orch, evoCfg.IdleTimeoutMinutes, evoCfg.ScanIntervalMinutes)
	return orch
}

// Start launches worker pool consumers and the background idle scanner.
func (o *EvolutionOrchestrator) Start(ctx context.Context) error {
	if !o.cfg.Enabled {
		slog.Info("Evolution subsystem is disabled in configuration")
		return nil
	}

	if !o.running.CompareAndSwap(false, true) {
		return errors.New("evolution orchestrator is already running")
	}

	// Launch worker pool (2 workers)
	for i := 0; i < 2; i++ {
		o.wg.Add(1)
		workerID := i + 1
		concurrency.SafeGo(func() {
			defer o.wg.Done()
			o.workerLoop(workerID)
		})
	}

	// Start Idle Scanner
	if o.scanner != nil {
		o.scanner.Start(o.ctx)
	}

	slog.InfoContext(ctx, "Evolution orchestrator started successfully",
		slog.Int("queue_capacity", o.cfg.QueueCapacity),
		slog.Int("idle_timeout_minutes", o.cfg.IdleTimeoutMinutes),
	)
	return nil
}

type sessionGen struct {
	id         int64
	lastAccess time.Time
}

func (o *EvolutionOrchestrator) getGenerationID(sessionKey string) int64 {
	if val, ok := o.genMap.Load(sessionKey); ok {
		switch v := val.(type) {
		case sessionGen:
			return v.id
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return 1
}

// NotifyUserActivity registers user interaction, bumping the generation ID and aborting stale reflections.
func (o *EvolutionOrchestrator) NotifyUserActivity(sessionKey string) {
	if sessionKey == "" {
		return
	}

	// Bounded Map Eviction (P4): Prune dormant sessions (>24h) if map exceeds 5000 items
	var count int
	o.genMap.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count > 5000 {
		cutoff := time.Now().Add(-24 * time.Hour)
		o.genMap.Range(func(key, value interface{}) bool {
			if sg, ok := value.(sessionGen); ok && sg.lastAccess.Before(cutoff) {
				o.genMap.Delete(key)
			}
			return true
		})
	}

	// Increment generation ID
	var currentGen int64 = 1
	if _, ok := o.genMap.Load(sessionKey); ok {
		currentGen = o.getGenerationID(sessionKey) + 1
	}
	o.genMap.Store(sessionKey, sessionGen{id: currentGen, lastAccess: time.Now()})

	// Signal abort to any active reflection on this session
	if val, ok := o.abortMap.LoadAndDelete(sessionKey); ok {
		abortChan := val.(chan struct{})
		select {
		case <-abortChan:
		default:
			close(abortChan)
		}
		slog.Debug("Aborted in-flight reflection because user resumed activity", slog.String("session_key", sessionKey))
	}
}

// TriggerConversationEvolution enqueues a conversation for async reflection and evolution.
func (o *EvolutionOrchestrator) TriggerConversationEvolution(ctx context.Context, convID string, trigger domain.EvolutionTrigger) error {
	if !o.cfg.Enabled || !o.running.Load() {
		return nil
	}
	if strings.TrimSpace(convID) == "" {
		return nil
	}

	// Load Conversation from Storage
	conv, err := o.storage.GetConversation(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to retrieve conversation %s: %w", convID, err)
	}

	// Retrieve turn logs/audit logs for this session
	audits, _ := o.storage.ListAuditLogs(ctx, conv.SessionKey, 20)

	// Resolve agent workspace path
	workspaceDir := config.ResolveAgentWorkspace(o.agentsDir, conv.AgentName)
	if conv.ProjectName != "" {
		projID := domain.FormatProjectID(conv.AgentName, conv.ProjectName)
		if proj, err := o.storage.GetProject(ctx, projID); err == nil && proj != nil {
			workspaceDir = proj.ProjectPath
		}
	}

	currentGen := o.getGenerationID(conv.SessionKey)

	snapshot := domain.ConversationSnapshot{
		ConversationID:    conv.ID,
		SessionKey:        conv.SessionKey,
		AgentName:         conv.AgentName,
		ProjectName:       conv.ProjectName,
		WorkspaceDir:      workspaceDir,
		GenerationID:      currentGen,
		AuditEntries:      audits,
		LastReflectedStep: conv.LastReflectedStep,
		CurrentStep:       conv.TurnCount,
		Trigger:           trigger,
		Timestamp:         time.Now(),
	}

	task := evolutionTask{
		snapshot: snapshot,
		trigger:  trigger,
	}

	// Non-blocking enqueue with load-shedding
	select {
	case o.queue <- task:
		slog.DebugContext(ctx, "Enqueued conversation evolution task",
			slog.String("conversation_id", convID),
			slog.String("trigger", string(trigger)),
		)
		return nil
	default:
		slog.WarnContext(ctx, "Evolution queue full, shedding load", slog.String("conversation_id", convID))
		return nil
	}
}

func (o *EvolutionOrchestrator) workerLoop(workerID int) {
	for {
		select {
		case <-o.ctx.Done():
			return
		case task, ok := <-o.queue:
			if !ok {
				return
			}
			o.processTask(task)
		}
	}
}

func (o *EvolutionOrchestrator) processTask(task evolutionTask) {
	snapshot := task.snapshot
	ctx := o.ctx

	// 1. Task State Guard: Check if task is paused/in-progress
	taskState := o.taskGuard.EvaluateTaskState(snapshot)
	if taskState == domain.TaskStateInProgressPaused || taskState == domain.TaskStateEphemeral {
		slog.Debug("Skipping reflection: task is in-progress or ephemeral",
			slog.String("conversation_id", snapshot.ConversationID),
			slog.String("state", string(taskState)),
		)
		return
	}

	// 2. Zero-LLM Pre-Filter: Check if conversation contains any learning signals
	shouldReflect, score, reason := o.filter.ShouldReflect(ctx, snapshot)
	if !shouldReflect || score < o.cfg.ConfidenceThreshold {
		slog.Debug("Zero-LLM Pre-Filter rejected reflection (0 tokens)",
			slog.String("conversation_id", snapshot.ConversationID),
			slog.Float64("score", score),
			slog.String("reason", reason),
		)
		// Update cursor to prevent re-scanning unchanged steps
		if err := o.memory.UpdateReflectedStep(ctx, snapshot.ConversationID, snapshot.CurrentStep); err != nil {
			slog.Warn("Failed to update evolution cursor after pre-filter skip",
				slog.String("conversation_id", snapshot.ConversationID),
				slog.Int("step", snapshot.CurrentStep),
				slog.Any("error", err),
			)
		}
		return
	}

	// 3. Register Abort Signal for active reflection
	abortChan := make(chan struct{})
	o.abortMap.Store(snapshot.SessionKey, abortChan)
	defer o.abortMap.Delete(snapshot.SessionKey)

	// Check if Generation ID changed before starting
	if o.getGenerationID(snapshot.SessionKey) != snapshot.GenerationID {
		slog.Debug("Generation changed before reflection start, aborting", slog.String("session_key", snapshot.SessionKey))
		return
	}

	// 4. Run Reflection Engine
	candidates, err := o.reflection.Reflect(ctx, snapshot, abortChan)
	if err != nil {
		slog.Warn("Reflection failed", slog.String("conversation_id", snapshot.ConversationID), slog.Any("error", err))
		// R2: Retry Queue for reflection failure (up to 2 retries)
		if task.retryCount < 2 && o.running.Load() {
			task.retryCount++
			time.AfterFunc(30*time.Second, func() {
				select {
				case o.queue <- task:
					slog.Debug("Re-enqueued failed reflection task", slog.String("conversation_id", snapshot.ConversationID), slog.Int("retry", task.retryCount))
				default:
				}
			})
		}
		return
	}
	if len(candidates) == 0 {
		return
	}

	// Check if Generation ID changed during reflection
	if o.getGenerationID(snapshot.SessionKey) != snapshot.GenerationID {
		slog.Debug("Generation changed during reflection, discarding results", slog.String("session_key", snapshot.SessionKey))
		return
	}

	// 5. Persistence Routing:
	// Always append to Daily Log (Soft Note)
	for _, cand := range candidates {
		_ = o.memory.AppendDailyLog(ctx, snapshot.WorkspaceDir, cand)
	}

	// If Explicit Switch or Task Done -> Promote to 4D MEMORY.md (Hard Promotion)
	if task.trigger == domain.TriggerExplicitSwitch || task.trigger == domain.TriggerTaskDone {
		_ = o.memory.PromoteToDurable4D(ctx, snapshot.WorkspaceDir, candidates)
	}

	// 6. Update Reflection Step Cursor with error logging
	if err := o.memory.UpdateReflectedStep(ctx, snapshot.ConversationID, snapshot.CurrentStep); err != nil {
		slog.Error("Failed to update evolution cursor after reflection",
			slog.String("conversation_id", snapshot.ConversationID),
			slog.Int("step", snapshot.CurrentStep),
			slog.Any("error", err),
		)
	}

	slog.Info("Successfully evolved memory from conversation",
		slog.String("conversation_id", snapshot.ConversationID),
		slog.Int("candidates_count", len(candidates)),
		slog.String("trigger", string(task.trigger)),
	)
}

// Stop halts workers and scanners cleanly.
func (o *EvolutionOrchestrator) Stop(ctx context.Context) error {
	if !o.running.CompareAndSwap(true, false) {
		return nil
	}

	if o.scanner != nil {
		o.scanner.Stop()
	}

	o.cancel()

	done := make(chan struct{})
	concurrency.SafeGo(func() {
		o.wg.Wait()
		close(done)
	})

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
