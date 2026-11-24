package compact

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

// imageMessage builds a user message carrying one base64 image of the given
// payload size plus optional text.
func imageMessage(payloadBytes int, text string) llm.Message {
	content := []llm.ContentBlock{{
		Type: "image",
		Source: &llm.ImageSource{
			Type:      "base64",
			MediaType: "image/jpeg",
			Data:      strings.Repeat("A", payloadBytes),
		},
	}}
	if text != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
	}
	return llm.Message{Role: llm.RoleUser, Content: content}
}

func TestEstimateTokens(t *testing.T) {
	// 400 characters is 100 estimated tokens.
	text := strings.Repeat("x", 400)

	tests := []struct {
		name    string
		msgs    []llm.Message
		minWant int
		maxWant int
	}{
		{
			name:    "empty",
			msgs:    nil,
			minWant: 0,
			maxWant: 0,
		},
		{
			name:    "text only",
			msgs:    []llm.Message{userText(text)},
			minWant: 100,
			maxWant: 100,
		},
		{
			// The whole point of the reserve: a 300 KB image must not estimate
			// as 75k tokens of base64 text.
			name:    "one 300KB image",
			msgs:    []llm.Message{imageMessage(300_000, "")},
			minWant: ImageTokenReserve,
			maxWant: ImageTokenReserve,
		},
		{
			name:    "image with text",
			msgs:    []llm.Message{imageMessage(300_000, text)},
			minWant: ImageTokenReserve + 100,
			maxWant: ImageTokenReserve + 100,
		},
		{
			name: "two images",
			msgs: []llm.Message{
				imageMessage(10_000, ""),
				imageMessage(400_000, ""),
			},
			minWant: 2 * ImageTokenReserve,
			maxWant: 2 * ImageTokenReserve,
		},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.msgs)
		if got < tt.minWant || got > tt.maxWant {
			t.Errorf("%s: EstimateTokens = %d, want %d..%d", tt.name, got, tt.minWant, tt.maxWant)
		}
	}
}

// TestEstimateTokensImageStopsTrackingPayloadSize pins the property the bug was
// about: doubling the base64 must not double the estimate.
func TestEstimateTokensImageStopsTrackingPayloadSize(t *testing.T) {
	small := EstimateTokens([]llm.Message{imageMessage(1_000, "")})
	large := EstimateTokens([]llm.Message{imageMessage(1_000_000, "")})
	if small != large {
		t.Fatalf("estimate tracks payload size: %d for 1KB, %d for 1MB", small, large)
	}
	if small != ImageTokenReserve {
		t.Fatalf("image estimate = %d, want the %d-token reserve", small, ImageTokenReserve)
	}
}

// TestEstimateTokensLargeImageStaysUnderDefaultTrigger is the regression guard
// for the reported failure: one screenshot plus a little text used to estimate
// past 140k tokens and trigger compaction on the next turn.
func TestEstimateTokensLargeImageStaysUnderDefaultTrigger(t *testing.T) {
	trigger := TriggerTokensForContextWindow(DefaultContextWindowTokens)
	msgs := []llm.Message{
		userText("here is a screenshot"),
		imageMessage(400_000, "what is wrong with this error?"),
	}
	if got := EstimateTokens(msgs); got >= trigger {
		t.Fatalf("estimate = %d tokens, want under the %d-token trigger", got, trigger)
	}
}
