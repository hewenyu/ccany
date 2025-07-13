package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// StreamingService handles Claude Code compatible streaming responses
type StreamingService struct {
	logger *logrus.Logger
}

// StreamingContext holds context for a streaming request
type StreamingContext struct {
	RequestID        string
	Model            string
	ContentBuffer    *bytes.Buffer
	MessageID        string
	StartTime        time.Time
	CurrentToolCalls map[string]*ToolCallState // Track tool calls by index
	TextBlockIndex   int
	ToolBlockCounter int
}

// ToolCallState tracks the state of a tool call during streaming
type ToolCallState struct {
	ID          string
	Name        string
	ArgsBuffer  string
	JSONSent    bool
	ClaudeIndex int
	Started     bool
}

// NewStreamingService creates a new streaming service
func NewStreamingService(logger *logrus.Logger) *StreamingService {
	return &StreamingService{
		logger: logger,
	}
}

// InitializeStreaming initializes Claude Code compatible streaming
func (s *StreamingService) InitializeStreaming(c *gin.Context, requestID, model string) *StreamingContext {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "*")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering

	messageID := fmt.Sprintf("msg_%s", uuid.New().String()[:24])

	streamCtx := &StreamingContext{
		RequestID:        requestID,
		Model:            model,
		ContentBuffer:    &bytes.Buffer{},
		MessageID:        messageID,
		StartTime:        time.Now(),
		CurrentToolCalls: make(map[string]*ToolCallState),
		TextBlockIndex:   0,
		ToolBlockCounter: 0,
	}

	// Send message_start event - matches claude-code-proxy exactly
	messageStartEvent := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}
	s.writeSSEEvent(c, "message_start", messageStartEvent)

	// Send content_block_start event
	contentBlockStartEvent := map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
	s.writeSSEEvent(c, "content_block_start", contentBlockStartEvent)

	// Send ping event
	pingEvent := map[string]interface{}{
		"type": "ping",
	}
	s.writeSSEEvent(c, "ping", pingEvent)

	// Flush immediately
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	return streamCtx
}

// ProcessTextChunk processes a text chunk and sends Claude Code compatible delta
func (s *StreamingService) ProcessTextChunk(c *gin.Context, streamCtx *StreamingContext, text string) {
	if text == "" {
		s.logger.WithFields(logrus.Fields{
			"request_id": streamCtx.RequestID,
			"model":      streamCtx.Model,
			"is_gemini":  strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		}).Debug("📝 Skipping empty text chunk")
		return
	}

	// Enhanced text processing logging
	s.logger.WithFields(logrus.Fields{
		"request_id":         streamCtx.RequestID,
		"model":              streamCtx.Model,
		"is_gemini":          strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		"text_length":        len(text),
		"text_content":       text,
		"buffer_size_before": streamCtx.ContentBuffer.Len(),
		"text_block_index":   streamCtx.TextBlockIndex,
	}).Debug("📄 Processing text chunk")

	// Add to content buffer
	streamCtx.ContentBuffer.WriteString(text)

	// Enhanced buffer state logging
	s.logger.WithFields(logrus.Fields{
		"request_id":        streamCtx.RequestID,
		"model":             streamCtx.Model,
		"is_gemini":         strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		"buffer_size_after": streamCtx.ContentBuffer.Len(),
		"total_content":     streamCtx.ContentBuffer.String(),
	}).Debug("📊 Content buffer updated")

	// Send content_block_delta event - matches claude-code-proxy format
	deltaEvent := map[string]interface{}{
		"type":  "content_block_delta",
		"index": streamCtx.TextBlockIndex,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}

	// Enhanced SSE event logging
	s.logger.WithFields(logrus.Fields{
		"request_id":  streamCtx.RequestID,
		"model":       streamCtx.Model,
		"is_gemini":   strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		"event_type":  "content_block_delta",
		"event_index": streamCtx.TextBlockIndex,
		"delta_type":  "text_delta",
		"delta_text":  text,
	}).Debug("📡 Sending content_block_delta SSE event")

	s.writeSSEEvent(c, "content_block_delta", deltaEvent)

	// Flush immediately
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
		s.logger.WithFields(logrus.Fields{
			"request_id": streamCtx.RequestID,
			"model":      streamCtx.Model,
			"is_gemini":  strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		}).Debug("🔄 Flushed SSE response")
	} else {
		s.logger.WithFields(logrus.Fields{
			"request_id": streamCtx.RequestID,
			"model":      streamCtx.Model,
			"is_gemini":  strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		}).Warn("⚠️ Failed to flush SSE response - writer does not support flushing")
	}
}

// ProcessToolCallDeltas processes tool call deltas from OpenAI streaming - based on claude-code-proxy
func (s *StreamingService) ProcessToolCallDeltas(c *gin.Context, streamCtx *StreamingContext, toolCallDeltas []interface{}) {
	// Enhanced tool call processing logging
	s.logger.WithFields(logrus.Fields{
		"request_id":               streamCtx.RequestID,
		"model":                    streamCtx.Model,
		"is_gemini":                strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
		"tool_calls_count":         len(toolCallDeltas),
		"current_tool_calls_state": len(streamCtx.CurrentToolCalls),
	}).Info("🔧 Starting tool call deltas processing")

	for i, tcDelta := range toolCallDeltas {
		s.logger.WithFields(logrus.Fields{
			"request_id":  streamCtx.RequestID,
			"model":       streamCtx.Model,
			"is_gemini":   strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
			"delta_index": i,
			"delta_type":  fmt.Sprintf("%T", tcDelta),
			"delta_value": tcDelta,
		}).Debug("🔨 Processing individual tool call delta")

		if tcDeltaMap, ok := tcDelta.(map[string]interface{}); ok {
			// Enhanced tool call delta parsing logging
			s.logger.WithFields(logrus.Fields{
				"request_id":  streamCtx.RequestID,
				"model":       streamCtx.Model,
				"is_gemini":   strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
				"delta_index": i,
				"delta_map_keys": func() []string {
					keys := make([]string, 0, len(tcDeltaMap))
					for k := range tcDeltaMap {
						keys = append(keys, k)
					}
					return keys
				}(),
			}).Debug("🗂️ Tool call delta map structure")

			// Get index as integer, not string
			indexFloat, ok := tcDeltaMap["index"].(float64)
			if !ok {
				s.logger.WithFields(logrus.Fields{
					"request_id":  streamCtx.RequestID,
					"model":       streamCtx.Model,
					"is_gemini":   strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
					"delta_index": i,
					"index_value": tcDeltaMap["index"],
					"index_type":  fmt.Sprintf("%T", tcDeltaMap["index"]),
				}).Warn("⚠️ Invalid tool call index type")
				continue
			}
			tcIndex := int(indexFloat)
			tcIndexStr := fmt.Sprintf("%d", tcIndex)

			s.logger.WithFields(logrus.Fields{
				"request_id":          streamCtx.RequestID,
				"model":               streamCtx.Model,
				"is_gemini":           strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
				"tool_call_index":     tcIndex,
				"tool_call_index_str": tcIndexStr,
			}).Debug("🔢 Tool call index parsed")

			// Initialize tool call tracking by index if not exists
			if _, exists := streamCtx.CurrentToolCalls[tcIndexStr]; !exists {
				streamCtx.CurrentToolCalls[tcIndexStr] = &ToolCallState{
					ArgsBuffer: "",
					JSONSent:   false,
					Started:    false,
				}
				s.logger.WithFields(logrus.Fields{
					"request_id":      streamCtx.RequestID,
					"model":           streamCtx.Model,
					"is_gemini":       strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
					"tool_call_index": tcIndex,
				}).Debug("🆕 Initialized new tool call state")
			}

			toolCall := streamCtx.CurrentToolCalls[tcIndexStr]

			// Enhanced tool call state logging
			s.logger.WithFields(logrus.Fields{
				"request_id":      streamCtx.RequestID,
				"model":           streamCtx.Model,
				"is_gemini":       strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
				"tool_call_index": tcIndex,
				"tool_call_id":    toolCall.ID,
				"tool_call_name":  toolCall.Name,
				"args_buffer_len": len(toolCall.ArgsBuffer),
				"json_sent":       toolCall.JSONSent,
				"started":         toolCall.Started,
			}).Debug("🔍 Current tool call state")

			// Update tool call ID if provided
			if id, exists := tcDeltaMap["id"]; exists {
				if idStr, ok := id.(string); ok && idStr != "" {
					oldID := toolCall.ID
					toolCall.ID = idStr
					s.logger.WithFields(logrus.Fields{
						"request_id":      streamCtx.RequestID,
						"model":           streamCtx.Model,
						"is_gemini":       strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
						"tool_call_index": tcIndex,
						"old_id":          oldID,
						"new_id":          idStr,
					}).Debug("🆔 Updated tool call ID")
				}
			}

			// Update function name and start content block if we have both id and name
			if function, exists := tcDeltaMap["function"]; exists {
				if functionMap, ok := function.(map[string]interface{}); ok {
					s.logger.WithFields(logrus.Fields{
						"request_id":      streamCtx.RequestID,
						"model":           streamCtx.Model,
						"is_gemini":       strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
						"tool_call_index": tcIndex,
						"function_keys": func() []string {
							keys := make([]string, 0, len(functionMap))
							for k := range functionMap {
								keys = append(keys, k)
							}
							return keys
						}(),
					}).Debug("⚙️ Processing function map")

					// Update function name
					if name, exists := functionMap["name"]; exists {
						if nameStr, ok := name.(string); ok && nameStr != "" {
							oldName := toolCall.Name
							toolCall.Name = nameStr
							s.logger.WithFields(logrus.Fields{
								"request_id":      streamCtx.RequestID,
								"model":           streamCtx.Model,
								"is_gemini":       strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
								"tool_call_index": tcIndex,
								"old_name":        oldName,
								"new_name":        nameStr,
							}).Debug("📝 Updated function name")
						}
					}

					// Start content block when we have complete initial data
					if toolCall.ID != "" && toolCall.Name != "" && !toolCall.Started {
						streamCtx.ToolBlockCounter++
						claudeIndex := streamCtx.TextBlockIndex + streamCtx.ToolBlockCounter
						toolCall.ClaudeIndex = claudeIndex
						toolCall.Started = true

						// Send content_block_start for tool use
						toolStartEvent := map[string]interface{}{
							"type":  "content_block_start",
							"index": claudeIndex,
							"content_block": map[string]interface{}{
								"type":  "tool_use",
								"id":    toolCall.ID,
								"name":  toolCall.Name,
								"input": map[string]interface{}{},
							},
						}
						s.writeSSEEvent(c, "content_block_start", toolStartEvent)
					}

					// Handle function arguments - match Python logic exactly
					if arguments, exists := functionMap["arguments"]; exists && toolCall.Started && arguments != nil {
						if argsStr, ok := arguments.(string); ok && argsStr != "" {
							toolCall.ArgsBuffer += argsStr

							// Try to parse complete JSON and send delta when we have valid JSON
							var parsedArgs interface{}
							if json.Unmarshal([]byte(toolCall.ArgsBuffer), &parsedArgs) == nil {
								// If parsing succeeds and we haven't sent this JSON yet
								if !toolCall.JSONSent {
									toolDeltaEvent := map[string]interface{}{
										"type":  "content_block_delta",
										"index": toolCall.ClaudeIndex,
										"delta": map[string]interface{}{
											"type":         "input_json_delta",
											"partial_json": toolCall.ArgsBuffer,
										},
									}
									s.writeSSEEvent(c, "content_block_delta", toolDeltaEvent)
									toolCall.JSONSent = true
								}
							}
							// If JSON is incomplete, continue accumulating
						}
					}
				}
			}
		}
	}

	// Flush immediately
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// FinalizeStreamingWithUsage finalizes streaming with accurate usage information
func (s *StreamingService) FinalizeStreamingWithUsage(c *gin.Context, streamCtx *StreamingContext, stopReason string, inputTokens, outputTokens int) {
	// Send content_block_stop event for text block
	contentBlockStopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": streamCtx.TextBlockIndex,
	}
	s.writeSSEEvent(c, "content_block_stop", contentBlockStopEvent)

	// Send content_block_stop events for all tool calls
	for _, toolCall := range streamCtx.CurrentToolCalls {
		if toolCall.Started {
			toolStopEvent := map[string]interface{}{
				"type":  "content_block_stop",
				"index": toolCall.ClaudeIndex,
			}
			s.writeSSEEvent(c, "content_block_stop", toolStopEvent)
		}
	}

	// Send message_delta event with stop reason and accurate usage
	messageDeltaEvent := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	s.writeSSEEvent(c, "message_delta", messageDeltaEvent)

	// Enhanced logging for usage information
	s.logger.WithFields(logrus.Fields{
		"request_id":     streamCtx.RequestID,
		"stop_reason":    stopReason,
		"input_tokens":   inputTokens,
		"output_tokens":  outputTokens,
		"content_length": streamCtx.ContentBuffer.Len(),
		"is_gemini":      strings.Contains(strings.ToLower(streamCtx.Model), "gemini"),
	}).Info("📊 Finalizing streaming with usage information")

	// Send message_stop event
	messageStopEvent := map[string]interface{}{
		"type": "message_stop",
	}
	s.writeSSEEvent(c, "message_stop", messageStopEvent)

	// Final flush
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// FinalizeStreaming finalizes streaming with default behavior (kept for backward compatibility)
func (s *StreamingService) FinalizeStreaming(c *gin.Context, streamCtx *StreamingContext, stopReason string) {
	// Call the enhanced version with zero tokens (fallback)
	s.FinalizeStreamingWithUsage(c, streamCtx, stopReason, 0, 0)
}

// HandleStreamingError handles streaming errors with Claude Code format
func (s *StreamingService) HandleStreamingError(c *gin.Context, streamCtx *StreamingContext, err error) {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "api_error",
			"message": err.Error(),
		},
	}
	s.writeSSEEvent(c, "error", errorEvent)

	// Ensure we still send proper termination events
	s.FinalizeStreaming(c, streamCtx, "error")
}

// UpdateUsageTokens updates token usage in the streaming context
func (s *StreamingService) UpdateUsageTokens(streamCtx *StreamingContext, inputTokens, outputTokens int) {
	// In the current implementation, we'll track these for final reporting
	// but Claude Code primarily gets usage information from the final events
}

// GetStreamingStats returns statistics about the streaming session
func (s *StreamingService) GetStreamingStats(streamCtx *StreamingContext) map[string]interface{} {
	return map[string]interface{}{
		"message_id":     streamCtx.MessageID,
		"request_id":     streamCtx.RequestID,
		"content_length": streamCtx.ContentBuffer.Len(),
		"duration_ms":    time.Since(streamCtx.StartTime).Milliseconds(),
	}
}

// CheckClientDisconnect checks if the client has disconnected
func (s *StreamingService) CheckClientDisconnect(c *gin.Context) bool {
	// Check if the client has disconnected by checking the context
	select {
	case <-c.Request.Context().Done():
		return true
	default:
		return false
	}
}

// SendPeriodicPing sends periodic ping events to keep the connection alive
func (s *StreamingService) SendPeriodicPing(c *gin.Context, ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Send ping every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingEvent := map[string]interface{}{
				"type": "ping",
			}
			s.writeSSEEvent(c, "ping", pingEvent)

			// Flush immediately
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

// writeSSEEvent writes a Server-Sent Event in the exact format expected by Claude Code
func (s *StreamingService) writeSSEEvent(c *gin.Context, event string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		s.logger.WithError(err).Error("Failed to marshal SSE event data")
		return
	}

	// Write event and data lines - matches claude-code-proxy format exactly
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(jsonData)); err != nil {
		s.logger.WithError(err).Debug("Failed to write SSE event - client may have disconnected")
	}
}
