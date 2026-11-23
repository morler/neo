package anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
)

// maxStreamLineBytes bounds one SSE data line. Deltas are small; a line beyond
// this means the stream is not what we think it is.
const maxStreamLineBytes = 1 << 20

// streamEvent is the union of the Messages API streaming events, decoded far
// enough to reassemble a complete response. Events we do not need (ping,
// content_block_stop for text) decode into an ignored type.
type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage *apiUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *llm.ContentBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *apiUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseStream reassembles a streaming response into the same value the
// non-streaming endpoint would have returned. Nothing is emitted as it
// arrives: Neo renders completed blocks, and streaming exists here so a long
// generation is not bounded by a single request timeout, not to paint text.
func parseStream(r io.Reader) (*llm.Response, error) {
	s := &streamState{toolJSON: map[int]*strings.Builder{}, blockAt: map[int]int{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLineBytes)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue // event: lines and blank separators carry nothing we need
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, retry.Permanent(fmt.Errorf("decode stream event: %w (data: %s)", err, payload))
		}
		if err := s.apply(ev); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	// A connection dropped part-way through ends in a clean EOF, so the absence
	// of the terminal event is the only signal that the response is partial.
	// Without this check a truncated stream arrives with no stop_reason, which
	// the agent loop reads as a normal end of turn — silent truncation is the
	// one failure mode worth being loud about.
	if !s.done {
		return nil, fmt.Errorf("anthropic: stream ended before the response was complete")
	}
	return &s.out, nil
}

// streamState accumulates one response across its events. Anthropic indexes
// content blocks by event, so both the block position and the tool-input
// fragments are tracked per index.
type streamState struct {
	out      llm.Response
	toolJSON map[int]*strings.Builder
	blockAt  map[int]int // event index -> position in out.Content
	done     bool
}

// apply folds one event into the response.
func (s *streamState) apply(ev streamEvent) error {
	if ev.Error != nil {
		return fmt.Errorf("anthropic: %s", ev.Error.Message)
	}
	switch ev.Type {
	case "message_start":
		s.startMessage(ev)
	case "content_block_start":
		s.startBlock(ev)
	case "content_block_delta":
		s.applyDelta(ev)
	case "content_block_stop":
		return s.stopBlock(ev)
	case "message_stop":
		s.done = true
	case "message_delta":
		s.applyMessageDelta(ev)
	}
	return nil
}

func (s *streamState) startMessage(ev streamEvent) {
	if ev.Message != nil && ev.Message.Usage != nil {
		s.out.Usage = ev.Message.Usage.toLLM()
	}
}

func (s *streamState) startBlock(ev streamEvent) {
	if ev.ContentBlock == nil {
		return
	}
	s.blockAt[ev.Index] = len(s.out.Content)
	s.out.Content = append(s.out.Content, *ev.ContentBlock)
	if ev.ContentBlock.Type == "tool_use" {
		s.toolJSON[ev.Index] = &strings.Builder{}
	}
}

func (s *streamState) applyDelta(ev streamEvent) {
	pos, ok := s.blockAt[ev.Index]
	if !ok || ev.Delta == nil {
		return
	}
	switch ev.Delta.Type {
	case "text_delta":
		s.out.Content[pos].Text += ev.Delta.Text
	case "input_json_delta":
		if b := s.toolJSON[ev.Index]; b != nil {
			b.WriteString(ev.Delta.PartialJSON)
		}
	}
}

// stopBlock attaches the tool input assembled from the deltas. Tool inputs
// arrive as concatenated JSON fragments; an empty buffer means a tool with no
// arguments, which is valid.
func (s *streamState) stopBlock(ev streamEvent) error {
	pos, ok := s.blockAt[ev.Index]
	if !ok {
		return nil
	}
	b := s.toolJSON[ev.Index]
	if b == nil {
		return nil
	}
	if raw := b.String(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.out.Content[pos].Input); err != nil {
			return retry.Permanent(fmt.Errorf("decode tool input for %s: %w", s.out.Content[pos].Name, err))
		}
	}
	return nil
}

// applyMessageDelta records the stop reason and the final output count. The
// input counts from message_start are already correct and must not be
// overwritten.
func (s *streamState) applyMessageDelta(ev streamEvent) {
	if ev.Delta != nil && ev.Delta.StopReason != "" {
		s.out.StopReason = ev.Delta.StopReason
	}
	if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
		s.out.Usage.OutputTokens = ev.Usage.OutputTokens
	}
}
