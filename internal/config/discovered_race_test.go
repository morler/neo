package config

import (
	"sync"
	"testing"
)

// TestDiscoveredContextWindowConcurrentAccess is the regression guard for #117.
// Catalogue discovery writes the map from a background goroutine while the
// subagent path reads it through ContextWindowFor on the main goroutine; a child
// agent built during a fetch used to abort the process with "concurrent map read
// and map write". Run with -race (CI does), this fails without discoveredMu.
func TestDiscoveredContextWindowConcurrentAccess(t *testing.T) {
	cfg := &Config{}

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(3)

	// The writer: one background discovery loop, as provider.go runs it.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			cfg.SetDiscoveredContextWindow("local", "m", 40_192+i)
		}
	}()
	// The reader the subagent runner uses when it builds a child.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			cfg.ContextWindowFor("local", "m")
		}
	}()
	// The reader with provenance, which the UI uses to explain compaction.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			cfg.ContextWindow("local", "m")
		}
	}()

	wg.Wait()

	if got, ok := cfg.DiscoveredContextWindow("local", "m"); !ok || got != 40_192+rounds-1 {
		t.Fatalf("discovered = %d/%v, want %d", got, ok, 40_192+rounds-1)
	}
}
