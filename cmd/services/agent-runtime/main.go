package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/lijunsheng/orderguard/internal/agentruntime"
	"github.com/lijunsheng/orderguard/internal/app"
	"github.com/lijunsheng/orderguard/internal/config"
	"github.com/lijunsheng/orderguard/internal/database"
)

// main 启动阶段 3 Agent Runtime。
func main() {
	ctx := context.Background()
	pool, err := database.OpenPostgres(ctx, config.DatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	registry := agentruntime.NewMCPRegistry(
		config.BusinessMCPURL(), config.ObservabilityMCPURL(), nil, config.KnowledgeMCPURL(),
	)
	if err := discoverWithRetry(ctx, registry, 30*time.Second); err != nil {
		log.Fatal(err)
	}
	repository := agentruntime.NewRepository(pool)
	chatModel, modelName, err := createModel(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var orchestrator *agentruntime.Orchestrator
	if chatModel == nil {
		orchestrator = agentruntime.NewOrchestrator(repository, nil, nil, slog.Default())
		slog.Warn("agent model is not configured; investigation creation is disabled")
	} else {
		planner := agentruntime.NewPlanner(chatModel, modelName, registry)
		investigator := agentruntime.NewInvestigator(chatModel, modelName, registry, repository)
		orchestrator = agentruntime.NewOrchestratorWithPhase4(
			repository, planner, investigator, registry, slog.Default(),
			chatModel,
		)
		go func() {
			if err := orchestrator.RunWorker(ctx); err != nil {
				slog.Error("agent runtime worker stopped", "error", err)
			}
		}()
	}
	app.RunHTTP(
		"agent-runtime", ":8080", agentruntime.NewHandler(repository, orchestrator),
	)
}

// createModel 根据环境变量创建 Eino OpenAI-compatible ChatModel。
func createModel(
	ctx context.Context,
) (model.ToolCallingChatModel, string, error) {
	modelName := config.AgentModel()
	if config.AgentAPIKey() == "" || modelName == "" {
		return nil, modelName, nil
	}
	chatModel, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey: config.AgentAPIKey(), BaseURL: config.AgentBaseURL(),
		Model: modelName, Timeout: config.AgentRequestTimeout(),
	})
	if err != nil {
		return nil, modelName, err
	}
	return chatModel, modelName, nil
}

// discoverWithRetry 等待两个只读 MCP Server 完成启动和工具发现。
func discoverWithRetry(
	ctx context.Context,
	registry *agentruntime.MCPRegistry,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := registry.Discover(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}
