package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RunToolLoop implements the ToolUser interface for Anthropic. It drives
// the Messages API's tool_use protocol turn-by-turn: send the current
// message history, inspect the reply for tool_use blocks, run them via
// req.Runner, and push the tool_result blocks back for the next turn.
//
// The loop stops when:
//   - the model's stop_reason is "end_turn" (no more tool calls), OR
//   - MaxTurns iterations have run (safety net for a model that never
//     decides it's done), OR
//   - the runner returns ErrAbort or any transport error.
//
// The returned string is the assistant's final text — the concatenation
// of every "text" content block in the last response.
func (p *anthropicProvider) RunToolLoop(ctx context.Context, req ToolLoopRequest) (string, error) {
	if req.Runner == nil {
		return "", fmt.Errorf("tool loop: Runner is nil")
	}
	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// Build the tools array once — it's identical every turn.
	tools := make([]anthropicToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, anthropicToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	// messages accumulates the conversation across turns. Content is
	// anthropicContent[] rather than a plain string so we can interleave
	// tool_result blocks without flipping shape.
	messages := []anthropicTurn{
		{Role: "user", Content: []anthropicContent{{Type: "text", Text: req.User}}},
	}

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := p.callMessages(ctx, messages, tools, req.System, req.Temperature, maxTokens)
		if err != nil {
			return "", err
		}

		// Assistant turn becomes part of the history for the next round so
		// the model's own tool_use blocks stay consistent with their IDs.
		messages = append(messages, anthropicTurn{Role: "assistant", Content: resp.Content})

		if resp.StopReason == "end_turn" || resp.StopReason == "" {
			// No more tool calls — return the concatenated text.
			return concatText(resp.Content), nil
		}

		// Collect every tool_use block the model emitted this turn.
		var calls []ToolCall
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			calls = append(calls, ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: block.Input,
			})
		}
		if len(calls) == 0 {
			// stop_reason said tool_use but no tool_use block surfaced —
			// treat as done rather than looping fruitlessly.
			return concatText(resp.Content), nil
		}

		// Run the tools and feed results back as a single user turn full
		// of tool_result blocks (the Messages API accepts multiple results
		// in one user message).
		results, err := req.Runner.Run(ctx, calls)
		if err != nil {
			// Runner propagates an abort signal — stop the loop and
			// surface whatever final text we have so far.
			return concatText(resp.Content), err
		}
		resultContent := make([]anthropicContent, 0, len(results))
		for _, r := range results {
			// A tool that accidentally returns a non-JSON-serialisable
			// value (channels, functions, cycles) would otherwise send a
			// silent "null" back to the model and hide the real failure.
			// Surface it as an explicit error tool_result instead so the
			// model can react.
			body, marshalErr := json.Marshal(r.Content)
			isError := r.IsError
			if marshalErr != nil {
				body, _ = json.Marshal(map[string]string{
					"error": fmt.Sprintf("failed to marshal tool result: %v", marshalErr),
				})
				isError = true
			}
			resultContent = append(resultContent, anthropicContent{
				Type:      "tool_result",
				ToolUseID: r.CallID,
				Content:   string(body),
				IsError:   isError,
			})
		}
		messages = append(messages, anthropicTurn{Role: "user", Content: resultContent})
	}

	// MaxTurns exhausted — return the last assistant text we saw.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return concatText(messages[i].Content), fmt.Errorf("tool loop hit max turns (%d) without end_turn", maxTurns)
		}
	}
	return "", fmt.Errorf("tool loop hit max turns (%d) without end_turn", maxTurns)
}

// concatText joins every text block in a response into a single string —
// the agent-facing final reply.
func concatText(content []anthropicContent) string {
	var buf bytes.Buffer
	for _, c := range content {
		if c.Type == "text" {
			buf.WriteString(c.Text)
		}
	}
	return buf.String()
}

// ─── Wire-format structs (Anthropic Messages API + tool_use) ─────────

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicTurn is one message in the conversation history. Content is a
// heterogeneous array of text / tool_use / tool_result blocks.
type anthropicTurn struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent covers every block type the loop produces or consumes.
// Fields are optional JSON-wise — unused ones get omitempty so a "text"
// block doesn't emit a nil Input, etc.
type anthropicContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// tool_use (assistant → caller)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result (caller → assistant)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicToolLoopRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicTurn    `json:"messages"`
	Tools       []anthropicToolDef `json:"tools,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicToolLoopResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callMessages does one round-trip against /v1/messages with the current
// history + tool catalogue. Isolated from RunToolLoop so the loop stays
// readable and tests can override Binary via httptest.
func (p *anthropicProvider) callMessages(
	ctx context.Context,
	messages []anthropicTurn,
	tools []anthropicToolDef,
	system string,
	temperature float64,
	maxTokens int,
) (*anthropicToolLoopResponse, error) {
	reqBody := anthropicToolLoopRequest{
		Model:       p.model,
		System:      system,
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}
	raw, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", p.apiVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic tool-loop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read anthropic response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr anthropicToolLoopResponse
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error != nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("anthropic tool-loop (status %d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("anthropic tool-loop (status %d)", resp.StatusCode)
	}

	var decoded anthropicToolLoopResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("anthropic tool-loop error: %s", decoded.Error.Message)
	}
	return &decoded, nil
}
