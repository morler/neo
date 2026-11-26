package main

import (
	"context"
	"fmt"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/subagent"
)

// subagentBackend resolves the optional worker backend. The zero-value config
// keeps the existing behavior: workers follow the coordinator. A configured
// backend stays independent, and credential/setup failures become worker
// failures so the coordinator can report them and continue.
func subagentBackend(ctx context.Context, cfg *config.Config, fallback llm.Provider, fallbackModel string) (llm.Provider, string, bool) {
	if cfg == nil || !cfg.SubagentsConfigured() {
		return fallback, fallbackModel, true
	}
	prov, err := checkedProvider(ctx, cfg, cfg.Subagents.Provider)
	if err != nil {
		prov = unavailableProvider{
			name: cfg.Subagents.Provider,
			err: fmt.Errorf("subagent backend %s/%s is unavailable: %w",
				cfg.Subagents.Provider, cfg.Subagents.Model, err),
		}
	}
	return prov, cfg.Subagents.Model, false
}

type unavailableProvider struct {
	name string
	err  error
}

func (p unavailableProvider) Name() string { return p.name }

func (p unavailableProvider) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return nil, p.err
}

// cfgEffort reads the effort from a possibly nil config.
func cfgEffort(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Effort()
}

// chatAgentTool builds the agent tool for an interactive chat session.
func chatAgentTool(prov llm.Provider, model, cwd, root string, cfg *config.Config) (subagent.AgentTool, <-chan subagent.Event, *subagent.AgentRunner) {
	runner := &subagent.AgentRunner{
		Provider:     prov,
		DefaultModel: model,
		// A child may run on a different model than the coordinator, so the
		// window is resolved when the worker is built rather than copied from
		// the parent's setting.
		WindowFor: func(provider, workerModel string) int {
			return cfg.ContextWindowFor(provider, workerModel)
		},
		Root:        root,
		BashTimeout: 5 * time.Minute,
		// Same decision as the coordinator's registry (registryFor), so a
		// session that enabled the tool does not end up with children that
		// cannot use it.
		Script:   cfg != nil && cfg.ScriptEnabled(),
		CodeMode: cfg == nil || !cfg.ClassicCodeMode(),
		// Children inherit the coordinator's thinking strength: a child that
		// reasons differently from its parent would make delegated work
		// unpredictable without the user asking for that split.
		Effort: cfgEffort(cfg),
	}
	sup := subagent.NewSupervisor(runner, subagent.DefaultBudget())
	return subagent.AgentTool{Sup: sup, Dir: cwd}, sup.Events, runner
}
