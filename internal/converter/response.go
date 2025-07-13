package converter

import (
	"fmt"
	"strings"

	"ccany/internal/models"

	"github.com/sirupsen/logrus"
)

// ConvertOpenAIToClaudeResponse converts OpenAI response to Claude format
func ConvertOpenAIToClaudeResponse(openaiResp *models.OpenAIChatCompletionResponse, originalReq *models.ClaudeMessagesRequest) (*models.ClaudeResponse, error) {
	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	choice := openaiResp.Choices[0]

	// Convert content
	content, err := convertMessageToClaudeContent(choice.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to convert message content: %w", err)
	}

	// Map finish reason
	stopReason := mapFinishReasonToClaudeStopReason(choice.FinishReason)

	claudeResp := &models.ClaudeResponse{
		ID:         openaiResp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      originalReq.Model, // Use original Claude model name
		StopReason: stopReason,
		Usage: models.ClaudeUsage{
			InputTokens:  openaiResp.Usage.PromptTokens,
			OutputTokens: openaiResp.Usage.CompletionTokens,
		},
	}

	return claudeResp, nil
}

// StreamingContext holds streaming state for proper Claude format conversion
type StreamingContext struct {
	MessageID       string
	Model           string
	InputTokens     int
	OutputTokens    int
	ContentStarted  bool
	ToolCallStarted bool
	CurrentToolCall map[string]interface{}
	ContentBuffer   string
}

// ConvertOpenAIStreamToClaudeStream converts OpenAI streaming response to Claude format
func ConvertOpenAIStreamToClaudeStream(openaiChunk *models.OpenAIStreamResponse, originalReq *models.ClaudeMessagesRequest, ctx *StreamingContext) ([]models.ClaudeStreamEvent, error) {
	var events []models.ClaudeStreamEvent

	// Enhanced conversion logging
	isGeminiModel := strings.Contains(strings.ToLower(originalReq.Model), "gemini")
	logger := logrus.WithFields(logrus.Fields{
		"model":         originalReq.Model,
		"is_gemini":     isGeminiModel,
		"chunk_id":      openaiChunk.ID,
		"chunk_model":   openaiChunk.Model,
		"choices_count": len(openaiChunk.Choices),
		"message_id":    ctx.MessageID,
	})

	logger.Debug("🔄 Starting OpenAI to Claude stream conversion")

	if len(openaiChunk.Choices) == 0 {
		logger.Debug("⚠️ No choices in OpenAI chunk - returning empty events")
		return events, nil
	}

	choice := openaiChunk.Choices[0]

	// Enhanced choice processing logging
	logger.WithFields(logrus.Fields{
		"choice_index":     choice.Index,
		"delta_role":       choice.Delta.Role,
		"delta_content":    choice.Delta.Content,
		"finish_reason":    choice.FinishReason,
		"tool_calls_count": len(choice.Delta.ToolCalls),
		"content_started":  ctx.ContentStarted,
	}).Debug("📝 Processing choice data")

	// Handle content block start if needed
	if choice.Delta.Content != "" && !ctx.ContentStarted {
		startEvent := models.ClaudeStreamEvent{
			Type:  "content_block_start",
			Index: 0,
			ContentBlock: &models.ClaudeContentBlock{
				Type: "text",
				Text: "",
			},
		}
		events = append(events, startEvent)
		ctx.ContentStarted = true

		logger.WithFields(logrus.Fields{
			"event_type":      "content_block_start",
			"event_index":     0,
			"content_started": ctx.ContentStarted,
		}).Debug("📦 Added content_block_start event")
	}

	// Handle text content delta
	if choice.Delta.Content != "" {
		// Estimate output tokens from content
		contentTokens := estimateContentTokens(choice.Delta.Content, isGeminiModel)
		ctx.OutputTokens += contentTokens

		// Add content to buffer
		ctx.ContentBuffer += choice.Delta.Content

		deltaEvent := models.ClaudeStreamEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &models.ClaudeContentBlock{
				Type: "text_delta",
				Text: choice.Delta.Content,
			},
		}
		events = append(events, deltaEvent)

		logger.WithFields(logrus.Fields{
			"event_type":       "content_block_delta",
			"event_index":      0,
			"content_length":   len(choice.Delta.Content),
			"content_tokens":   contentTokens,
			"total_out_tokens": ctx.OutputTokens,
			"content_chunk":    choice.Delta.Content,
		}).Debug("📄 Added content_block_delta event with token calculation")
	}

	// Handle tool calls
	if len(choice.Delta.ToolCalls) > 0 {
		logger.WithFields(logrus.Fields{
			"tool_calls_count": len(choice.Delta.ToolCalls),
			"tool_calls":       choice.Delta.ToolCalls,
		}).Debug("🔧 Processing tool calls in stream conversion")

		// Process tool calls and estimate tokens
		for _, toolCall := range choice.Delta.ToolCalls {
			toolCallTokens := estimateToolCallTokens(toolCall, isGeminiModel)
			ctx.OutputTokens += toolCallTokens

			// Create tool call events (simplified for streaming)
			if toolCall.Function.Name != "" {
				toolEvent := models.ClaudeStreamEvent{
					Type:  "content_block_start",
					Index: 1, // Tool calls typically start at index 1
					ContentBlock: &models.ClaudeContentBlock{
						Type: "tool_use",
						ID:   toolCall.ID,
						Name: toolCall.Function.Name,
					},
				}
				events = append(events, toolEvent)
			}
		}
	}

	// Handle finish reason
	if choice.FinishReason != "" {
		logger.WithFields(logrus.Fields{
			"finish_reason":   choice.FinishReason,
			"content_started": ctx.ContentStarted,
		}).Info("🏁 Processing finish reason")

		// Send content block stop if content was started
		if ctx.ContentStarted {
			stopEvent := models.ClaudeStreamEvent{
				Type:  "content_block_stop",
				Index: 0,
			}
			events = append(events, stopEvent)

			logger.WithFields(logrus.Fields{
				"event_type":  "content_block_stop",
				"event_index": 0,
			}).Debug("📦 Added content_block_stop event")
		}

		stopReason := mapFinishReasonToClaudeStopReason(choice.FinishReason)

		logger.WithFields(logrus.Fields{
			"original_reason": choice.FinishReason,
			"mapped_reason":   stopReason,
		}).Debug("🗺️ Mapped finish reason")

		// Send message delta with stop reason and usage
		deltaEvent := models.ClaudeStreamEvent{
			Type: "message_delta",
			Delta: &models.ClaudeContentBlock{
				Type: "stop_reason",
				Text: stopReason,
			},
			Usage: &models.ClaudeUsage{
				InputTokens:  ctx.InputTokens,
				OutputTokens: ctx.OutputTokens,
			},
		}
		events = append(events, deltaEvent)

		logger.WithFields(logrus.Fields{
			"event_type":    "message_delta",
			"stop_reason":   stopReason,
			"input_tokens":  ctx.InputTokens,
			"output_tokens": ctx.OutputTokens,
		}).Debug("📦 Added message_delta event with stop reason and usage")

		// Send message stop
		stopEvent := models.ClaudeStreamEvent{
			Type: "message_stop",
		}
		events = append(events, stopEvent)

		logger.Debug("📦 Added message_stop event")
	}

	logger.WithFields(logrus.Fields{
		"events_count": len(events),
		"events_types": func() []string {
			types := make([]string, len(events))
			for i, event := range events {
				types[i] = event.Type
			}
			return types
		}(),
	}).Debug("✅ Completed OpenAI to Claude stream conversion")

	return events, nil
}

// estimateContentTokens estimates tokens from content text
func estimateContentTokens(content string, isGeminiModel bool) int {
	if content == "" {
		return 0
	}

	// Different token estimation for different models
	var charsPerToken int
	if isGeminiModel {
		charsPerToken = 3 // Gemini models tend to be more efficient
	} else {
		charsPerToken = 4 // Standard OpenAI models
	}

	// Basic word and character-based estimation
	words := strings.Fields(content)
	baseTokens := len(words) + len(content)/charsPerToken

	return max(1, baseTokens)
}

// estimateToolCallTokens estimates tokens from tool call data
func estimateToolCallTokens(toolCall models.OpenAIToolCallDelta, isGeminiModel bool) int {
	totalTokens := 0

	// Base overhead for tool call structure
	totalTokens += 15

	// Function name tokens
	if toolCall.Function.Name != "" {
		totalTokens += len(toolCall.Function.Name) / 4
	}

	// Function arguments tokens (JSON is more dense)
	if toolCall.Function.Arguments != "" {
		totalTokens += len(toolCall.Function.Arguments) / 3
	}

	// Gemini models might have slightly different overhead
	if isGeminiModel {
		totalTokens = int(float64(totalTokens) * 0.9) // 10% less overhead
	}

	return max(1, totalTokens)
}

// convertMessageToClaudeContent converts simple Message to Claude content blocks
func convertMessageToClaudeContent(msg models.Message) ([]models.ClaudeContentBlock, error) {
	var content []models.ClaudeContentBlock

	// Handle text content
	if msg.Content != "" {
		content = append(content, models.ClaudeContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}

	// If no content, add empty text block
	if len(content) == 0 {
		content = append(content, models.ClaudeContentBlock{
			Type: "text",
			Text: "",
		})
	}

	return content, nil
}

// mapFinishReasonToClaudeStopReason maps finish reasons to Claude stop reasons
func mapFinishReasonToClaudeStopReason(finishReason string) string {
	if finishReason == "" {
		return "end_turn"
	}

	switch finishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// CreateClaudeErrorResponse creates a Claude-formatted error response
func CreateClaudeErrorResponse(errorType, message string) *models.ClaudeErrorResponse {
	return &models.ClaudeErrorResponse{
		Type: "error",
		Error: models.ClaudeError{
			Type:    errorType,
			Message: message,
		},
	}
}

// CreateClaudeStreamStartEvent creates a stream start event
func CreateClaudeStreamStartEvent(messageID, model string) models.ClaudeStreamEvent {
	return models.ClaudeStreamEvent{
		Type: "message_start",
		Message: &models.ClaudeResponse{
			ID:           messageID,
			Type:         "message",
			Role:         "assistant",
			Model:        model,
			Content:      []models.ClaudeContentBlock{},
			StopReason:   "",
			StopSequence: nil,
			Usage: models.ClaudeUsage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		},
	}
}

// CreateClaudeStreamPingEvent creates a ping event for keep-alive
func CreateClaudeStreamPingEvent() models.ClaudeStreamEvent {
	return models.ClaudeStreamEvent{
		Type: "ping",
	}
}

// CreateStreamingContext creates a new streaming context
func CreateStreamingContext(messageID, model string, inputTokens int) *StreamingContext {
	return &StreamingContext{
		MessageID:       messageID,
		Model:           model,
		InputTokens:     inputTokens,
		OutputTokens:    0,
		ContentStarted:  false,
		ToolCallStarted: false,
		CurrentToolCall: make(map[string]interface{}),
		ContentBuffer:   "",
	}
}

// CreateClaudeStreamStopEvent creates a stream stop event
func CreateClaudeStreamStopEvent(usage models.ClaudeUsage) models.ClaudeStreamEvent {
	return models.ClaudeStreamEvent{
		Type:  "message_delta",
		Usage: &usage,
	}
}
