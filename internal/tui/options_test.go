package tui

import (
	"bytes"
	"context"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/workflow"
)

// TestOptionsApplyTheirFields pins each functional option to the Options field
// it writes. These constructors had no coverage at all, so a typo in one of
// them (writing the wrong field, or dropping a field) would have gone unseen
// until someone wired it up by hand.
func TestOptionsApplyTheirFields(t *testing.T) {
	t.Run("WithModelSwitcher", func(t *testing.T) {
		var switched string
		fn := func(model string) error {
			switched = model
			return nil
		}
		choices := []ModelChoice{{ID: "gpt-5.2", Name: "GPT-5.2"}}

		opts := Options{}
		WithModelSwitcher("openai", choices, fn)(&opts)

		if opts.Provider != "openai" {
			t.Fatalf("Provider = %q, want openai", opts.Provider)
		}
		if len(opts.ModelChoices) != 1 || opts.ModelChoices[0].ID != "gpt-5.2" {
			t.Fatalf("ModelChoices = %+v", opts.ModelChoices)
		}
		if opts.ModelSwitcher == nil {
			t.Fatal("ModelSwitcher is nil")
		}
		if err := opts.ModelSwitcher("gpt-4o"); err != nil {
			t.Fatalf("switcher returned %v", err)
		}
		if switched != "gpt-4o" {
			t.Fatalf("switcher called with %q, want gpt-4o", switched)
		}
	})

	t.Run("WithProviderSwitcher", func(t *testing.T) {
		var switched string
		fn := func(name string) (ProviderState, error) {
			switched = name
			return ProviderState{Model: "m", Choices: []ModelChoice{{ID: "m"}}}, nil
		}
		choices := []ProviderChoice{{ID: "anthropic", Name: "Anthropic"}}

		opts := Options{}
		WithProviderSwitcher(choices, fn)(&opts)

		if len(opts.ProviderChoices) != 1 || opts.ProviderChoices[0].ID != "anthropic" {
			t.Fatalf("ProviderChoices = %+v", opts.ProviderChoices)
		}
		if opts.ProviderSwitcher == nil {
			t.Fatal("ProviderSwitcher is nil")
		}
		state, err := opts.ProviderSwitcher("google")
		if err != nil {
			t.Fatalf("switcher returned %v", err)
		}
		if switched != "google" {
			t.Fatalf("switcher called with %q, want google", switched)
		}
		if state.Model != "m" {
			t.Fatalf("state.Model = %q, want m", state.Model)
		}
	})

	t.Run("WithModelChoicesLoader", func(t *testing.T) {
		called := false
		loader := func(context.Context) ([]ModelChoice, error) {
			called = true
			return []ModelChoice{{ID: "live"}}, nil
		}

		opts := Options{}
		WithModelChoicesLoader(loader)(&opts)

		if opts.ModelChoicesLoader == nil {
			t.Fatal("ModelChoicesLoader is nil")
		}
		choices, err := opts.ModelChoicesLoader(context.Background())
		if err != nil {
			t.Fatalf("loader returned %v", err)
		}
		if !called {
			t.Fatal("loader was not called")
		}
		if len(choices) != 1 || choices[0].ID != "live" {
			t.Fatalf("choices = %+v", choices)
		}
	})

	t.Run("WithWorkflowEvents", func(t *testing.T) {
		// A closed channel is enough: the option only stores it.
		ch := make(chan workflow.Event)
		close(ch)

		opts := Options{}
		WithWorkflowEvents(ch)(&opts)

		if opts.WorkflowEvents == nil {
			t.Fatal("WorkflowEvents is nil")
		}
		if _, ok := <-opts.WorkflowEvents; ok {
			t.Fatal("expected the stored channel to be the closed one")
		}
	})

	t.Run("WithVerbose", func(t *testing.T) {
		opts := Options{}
		WithVerbose(true)(&opts)
		if !opts.Verbose {
			t.Fatal("Verbose = false, want true")
		}
	})

	t.Run("WithIO", func(t *testing.T) {
		in := bytes.NewBufferString("typed")
		out := &bytes.Buffer{}

		opts := Options{}
		WithIO(in, out)(&opts)

		if opts.Input != in {
			t.Fatal("Input was not stored")
		}
		if opts.Output != out {
			t.Fatal("Output was not stored")
		}
	})
}

// TestNewModelWiresTheOptions checks the other side: newModel must carry the
// option values onto the model, so the browsers and rendering use them.
func TestNewModelWiresTheOptions(t *testing.T) {
	ag := agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}})
	var opts Options
	WithModelSwitcher("openai", []ModelChoice{{ID: "gpt-5.2", Name: "GPT-5.2"}}, func(string) error { return nil })(&opts)
	WithModelChoicesLoader(func(context.Context) ([]ModelChoice, error) { return nil, nil })(&opts)
	WithProviderSwitcher([]ProviderChoice{{ID: "anthropic", Name: "Anthropic"}}, func(string) (ProviderState, error) {
		return ProviderState{}, nil
	})(&opts)
	WithVerbose(true)(&opts)

	m := newModel(context.Background(), ag, "gpt-5.2", "test", "~", nil, opts)

	if m.modelSwitcher == nil {
		t.Fatal("modelSwitcher is nil")
	}
	if m.modelChoicesLoader == nil {
		t.Fatal("modelChoicesLoader is nil")
	}
	if m.providerSwitcher == nil {
		t.Fatal("providerSwitcher is nil")
	}
	if !m.verbose {
		t.Fatal("verbose = false, want true")
	}
	if len(m.modelChoices) == 0 {
		t.Fatal("modelChoices is empty")
	}
	if len(m.providerChoices) == 0 {
		t.Fatal("providerChoices is empty")
	}
}
