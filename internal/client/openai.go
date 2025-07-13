package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ccany/internal/models"

	"github.com/sashabaranov/go-openai"
	"github.com/sirupsen/logrus"
)

// OpenAIClient handles communication with OpenAI API using the official SDK
type OpenAIClient struct {
	client  *openai.Client
	apiKey  string
	baseURL string
	timeout time.Duration
	logger  *logrus.Logger
}

// NewOpenAIClient creates a new OpenAI client using the official SDK with proxy support
func NewOpenAIClient(apiKey, baseURL string, timeout int, logger *logrus.Logger) *OpenAIClient {
	return NewOpenAIClientWithProxy(apiKey, baseURL, timeout, nil, logger)
}

// NewOpenAIClientWithProxy creates a new OpenAI client with proxy support
func NewOpenAIClientWithProxy(apiKey, baseURL string, timeout int, proxyConfig *ProxyConfig, logger *logrus.Logger) *OpenAIClient {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" && baseURL != "https://api.openai.com/v1" {
		// For custom base URLs, we need to handle the path construction carefully
		// The OpenAI SDK always appends "/chat/completions" to the BaseURL
		// So we need to set BaseURL such that when SDK appends "/chat/completions",
		// the final URL matches what the service expects

		finalBaseURL := constructBaseURL(baseURL)
		config.BaseURL = finalBaseURL

		logger.WithFields(logrus.Fields{
			"original_base_url":    baseURL,
			"constructed_base_url": finalBaseURL,
			"final_endpoint":       finalBaseURL + "/chat/completions",
		}).Info("OpenAI client URL construction")
	}

	// Configure proxy if provided
	if proxyConfig != nil && proxyConfig.Enabled {
		transport := BuildHTTPTransport(proxyConfig)
		httpClient := &http.Client{
			Transport: transport,
		}

		logger.WithFields(logrus.Fields{
			"proxy_enabled": proxyConfig.Enabled,
			"proxy_type":    proxyConfig.Type,
			"ignore_ssl":    proxyConfig.IgnoreSSL,
		}).Info("OpenAI client configured with proxy")

		config.HTTPClient = httpClient
	}

	client := openai.NewClientWithConfig(config)

	return &OpenAIClient{
		client:  client,
		apiKey:  apiKey,
		baseURL: baseURL, // Store original base URL for reference
		timeout: time.Duration(timeout) * time.Second,
		logger:  logger,
	}
}

// CreateChatCompletion sends a chat completion request to OpenAI using the official SDK
func (c *OpenAIClient) CreateChatCompletion(ctx context.Context, req *models.OpenAIChatCompletionRequest) (*models.OpenAIChatCompletionResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("OpenAI client is not configured")
	}
	if c.logger == nil {
		return nil, fmt.Errorf("logger is not configured")
	}

	c.logger.WithFields(logrus.Fields{
		"model":      req.Model,
		"stream":     req.Stream,
		"max_tokens": req.MaxTokens,
	}).Debug("Sending OpenAI chat completion request")

	// Convert our request to OpenAI SDK format
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Stream:   req.Stream,
	}

	// Set optional fields with proper type conversion
	if req.MaxTokens != nil {
		openaiReq.MaxTokens = *req.MaxTokens
	}
	if req.Temperature != nil {
		openaiReq.Temperature = float32(*req.Temperature)
	}
	if req.TopP != nil {
		openaiReq.TopP = float32(*req.TopP)
	}
	if req.N != nil {
		openaiReq.N = *req.N
	}
	if req.Stop != nil {
		if stopSlice, ok := req.Stop.([]string); ok {
			openaiReq.Stop = stopSlice
		}
	}

	// Add timeout to context
	ctxWithTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.CreateChatCompletion(ctxWithTimeout, openaiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat completion: %w", err)
	}

	// Convert SDK response to our format
	result := &models.OpenAIChatCompletionResponse{
		ID:      resp.ID,
		Object:  resp.Object,
		Created: resp.Created,
		Model:   resp.Model,
		Choices: convertChoices(resp.Choices),
		Usage: models.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	c.logger.WithFields(logrus.Fields{
		"response_id":       result.ID,
		"model":             result.Model,
		"prompt_tokens":     result.Usage.PromptTokens,
		"completion_tokens": result.Usage.CompletionTokens,
	}).Debug("Received OpenAI chat completion response")

	return result, nil
}

// CreateChatCompletionStream sends a streaming chat completion request to OpenAI using the official SDK
func (c *OpenAIClient) CreateChatCompletionStream(ctx context.Context, req *models.OpenAIChatCompletionRequest) (<-chan StreamChunk, error) {
	if c == nil {
		return nil, fmt.Errorf("OpenAI client is not configured")
	}
	if c.logger == nil {
		return nil, fmt.Errorf("logger is not configured")
	}

	// Enhanced logging for gemini model detection
	isGeminiModel := strings.Contains(strings.ToLower(req.Model), "gemini")

	c.logger.WithFields(logrus.Fields{
		"model":          req.Model,
		"is_gemini":      isGeminiModel,
		"stream":         true,
		"max_tokens":     req.MaxTokens,
		"temperature":    req.Temperature,
		"top_p":          req.TopP,
		"messages_count": len(req.Messages),
		"base_url":       c.baseURL,
		"final_endpoint": c.GetFinalEndpointURL(),
	}).Info("🚀 Sending OpenAI streaming chat completion request")

	// Log detailed request structure for gemini debugging
	if c.logger.IsLevelEnabled(logrus.DebugLevel) {
		reqJSON, _ := json.Marshal(req)
		c.logger.WithFields(logrus.Fields{
			"model":           req.Model,
			"is_gemini":       isGeminiModel,
			"request_payload": string(reqJSON),
			"api_key_prefix": func() string {
				if len(c.apiKey) > 8 {
					return c.apiKey[:8] + "..."
				} else {
					return "short_key"
				}
			}(),
		}).Debug("📋 Detailed OpenAI request payload")
	}

	// Convert our request to OpenAI SDK format
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Stream:   true,
	}

	// Set optional fields with proper type conversion
	if req.MaxTokens != nil {
		openaiReq.MaxTokens = *req.MaxTokens
		c.logger.WithFields(logrus.Fields{
			"model":      req.Model,
			"is_gemini":  isGeminiModel,
			"max_tokens": *req.MaxTokens,
		}).Debug("🔧 Set max tokens")
	}
	if req.Temperature != nil {
		openaiReq.Temperature = float32(*req.Temperature)
		c.logger.WithFields(logrus.Fields{
			"model":       req.Model,
			"is_gemini":   isGeminiModel,
			"temperature": *req.Temperature,
		}).Debug("🔧 Set temperature")
	}
	if req.TopP != nil {
		openaiReq.TopP = float32(*req.TopP)
		c.logger.WithFields(logrus.Fields{
			"model":     req.Model,
			"is_gemini": isGeminiModel,
			"top_p":     *req.TopP,
		}).Debug("🔧 Set top_p")
	}
	if req.N != nil {
		openaiReq.N = *req.N
		c.logger.WithFields(logrus.Fields{
			"model":     req.Model,
			"is_gemini": isGeminiModel,
			"n":         *req.N,
		}).Debug("🔧 Set n")
	}
	if req.Stop != nil {
		if stopSlice, ok := req.Stop.([]string); ok {
			openaiReq.Stop = stopSlice
			c.logger.WithFields(logrus.Fields{
				"model":     req.Model,
				"is_gemini": isGeminiModel,
				"stop":      stopSlice,
			}).Debug("🔧 Set stop sequences")
		}
	}

	// Enhanced logging for OpenAI SDK request
	if c.logger.IsLevelEnabled(logrus.DebugLevel) {
		openaiReqJSON, _ := json.Marshal(openaiReq)
		c.logger.WithFields(logrus.Fields{
			"model":              req.Model,
			"is_gemini":          isGeminiModel,
			"openai_sdk_request": string(openaiReqJSON),
		}).Debug("📡 OpenAI SDK request structure")
	}

	// For streaming requests, use the provided context directly to avoid double timeout
	// The handler already sets appropriate timeout based on request type
	c.logger.WithFields(logrus.Fields{
		"model":     req.Model,
		"is_gemini": isGeminiModel,
	}).Info("🔄 Creating OpenAI SDK stream connection")

	stream, err := c.client.CreateChatCompletionStream(ctx, openaiReq)
	if err != nil {
		c.logger.WithFields(logrus.Fields{
			"model":     req.Model,
			"is_gemini": isGeminiModel,
			"error":     err.Error(),
		}).Error("❌ Failed to create OpenAI SDK stream connection")
		return nil, fmt.Errorf("failed to create chat completion stream: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"model":     req.Model,
		"is_gemini": isGeminiModel,
	}).Info("✅ OpenAI SDK stream connection established")

	streamChan := make(chan StreamChunk, 10)

	go func() {
		defer close(streamChan)
		defer stream.Close()

		chunkCount := 0
		c.logger.WithFields(logrus.Fields{
			"model":     req.Model,
			"is_gemini": isGeminiModel,
		}).Info("🎯 Starting OpenAI SDK stream processing")

		for {
			select {
			case <-ctx.Done():
				c.logger.WithFields(logrus.Fields{
					"model":       req.Model,
					"is_gemini":   isGeminiModel,
					"chunk_count": chunkCount,
					"context_err": ctx.Err(),
				}).Warn("⏰ OpenAI SDK stream context cancelled")
				streamChan <- StreamChunk{Error: ctx.Err(), Done: true}
				return
			default:
			}

			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					c.logger.WithFields(logrus.Fields{
						"model":       req.Model,
						"is_gemini":   isGeminiModel,
						"chunk_count": chunkCount,
					}).Info("✅ OpenAI SDK stream completed (EOF)")
					streamChan <- StreamChunk{Done: true}
					return
				}
				c.logger.WithFields(logrus.Fields{
					"model":       req.Model,
					"is_gemini":   isGeminiModel,
					"chunk_count": chunkCount,
					"error":       err.Error(),
				}).Error("❌ OpenAI SDK stream receive error")
				streamChan <- StreamChunk{Error: err, Done: true}
				return
			}

			chunkCount++

			// Enhanced response logging
			c.logger.WithFields(logrus.Fields{
				"model":          req.Model,
				"is_gemini":      isGeminiModel,
				"chunk_number":   chunkCount,
				"response_id":    response.ID,
				"response_model": response.Model,
				"choices_count":  len(response.Choices),
			}).Debug("📦 OpenAI SDK response chunk received")

			// Log detailed response structure for gemini debugging
			if c.logger.IsLevelEnabled(logrus.DebugLevel) {
				responseJSON, _ := json.Marshal(response)
				c.logger.WithFields(logrus.Fields{
					"model":            req.Model,
					"is_gemini":        isGeminiModel,
					"chunk_number":     chunkCount,
					"response_payload": string(responseJSON),
				}).Debug("📊 OpenAI SDK response details")
			}

			// Convert SDK stream response to our format
			streamResp := &models.OpenAIStreamResponse{
				ID:      response.ID,
				Object:  response.Object,
				Created: response.Created,
				Model:   response.Model,
				Choices: convertStreamChoices(response.Choices),
			}

			// Enhanced conversion logging
			c.logger.WithFields(logrus.Fields{
				"model":           req.Model,
				"is_gemini":       isGeminiModel,
				"chunk_number":    chunkCount,
				"original_model":  response.Model,
				"converted_model": streamResp.Model,
				"choices_count":   len(streamResp.Choices),
			}).Debug("🔄 Converting OpenAI SDK response to internal format")

			// Log choice details for gemini debugging
			if len(streamResp.Choices) > 0 {
				choice := streamResp.Choices[0]
				c.logger.WithFields(logrus.Fields{
					"model":            req.Model,
					"is_gemini":        isGeminiModel,
					"chunk_number":     chunkCount,
					"choice_index":     choice.Index,
					"delta_role":       choice.Delta.Role,
					"delta_content":    choice.Delta.Content,
					"finish_reason":    choice.FinishReason,
					"tool_calls_count": len(choice.Delta.ToolCalls),
				}).Debug("📝 Choice delta details")

				// Log tool calls if present
				if len(choice.Delta.ToolCalls) > 0 {
					for i, tc := range choice.Delta.ToolCalls {
						c.logger.WithFields(logrus.Fields{
							"model":          req.Model,
							"is_gemini":      isGeminiModel,
							"chunk_number":   chunkCount,
							"tool_call_idx":  i,
							"tool_call_id":   tc.ID,
							"tool_call_type": tc.Type,
							"function_name":  tc.Function.Name,
							"function_args":  tc.Function.Arguments,
						}).Debug("🔧 Tool call delta details")
					}
				}
			}

			streamChan <- StreamChunk{Data: streamResp}
		}
	}()

	return streamChan, nil
}

// StreamChunk represents a chunk from the streaming response
type StreamChunk struct {
	Data  *models.OpenAIStreamResponse
	Error error
	Done  bool
}

// convertMessages converts our message format to OpenAI SDK format
func convertMessages(messages []models.Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		result[i] = openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return result
}

// convertChoices converts OpenAI SDK choices to our format
func convertChoices(choices []openai.ChatCompletionChoice) []models.Choice {
	result := make([]models.Choice, len(choices))
	for i, choice := range choices {
		result[i] = models.Choice{
			Index: choice.Index,
			Message: models.Message{
				Role:    choice.Message.Role,
				Content: choice.Message.Content,
			},
			FinishReason: string(choice.FinishReason),
		}
	}
	return result
}

// convertStreamChoices converts OpenAI SDK stream choices to our format
func convertStreamChoices(choices []openai.ChatCompletionStreamChoice) []models.StreamChoice {
	result := make([]models.StreamChoice, len(choices))
	for i, choice := range choices {
		result[i] = models.StreamChoice{
			Index: choice.Index,
			Delta: models.StreamDelta{
				Role:    choice.Delta.Role,
				Content: choice.Delta.Content,
			},
			FinishReason: string(choice.FinishReason),
		}
	}
	return result
}

// ClassifyError classifies OpenAI API errors
func (c *OpenAIClient) ClassifyError(err error) string {
	if c == nil || err == nil {
		return "unknown_error"
	}

	errStr := err.Error()

	if strings.Contains(errStr, "rate limit") {
		return "rate_limit_error"
	}
	if strings.Contains(errStr, "insufficient_quota") || strings.Contains(errStr, "quota") {
		return "insufficient_quota"
	}
	if strings.Contains(errStr, "invalid_api_key") || strings.Contains(errStr, "authentication") {
		return "authentication_error"
	}
	if strings.Contains(errStr, "model_not_found") || strings.Contains(errStr, "model") {
		return "not_found_error"
	}
	if strings.Contains(errStr, "context_length") || strings.Contains(errStr, "too long") {
		return "invalid_request_error"
	}

	return "api_error"
}

// constructBaseURL constructs the final base URL for OpenAI SDK
// The SDK will automatically append "/chat/completions" to whatever BaseURL we provide
// Smart rule:
// 1. If URL ends with "/" - remove trailing slash
// 2. If URL already contains "/v1" in the path - don't add another /v1
// 3. If URL has multiple path segments (indicates API service structure) - don't add /v1
// 4. Otherwise - append "/v1" for standard OpenAI-compatible services
func constructBaseURL(baseURL string) string {
	if baseURL == "" {
		return "https://api.openai.com/v1"
	}

	// Handle trailing slash - always remove it
	if strings.HasSuffix(baseURL, "/") {
		baseURL = strings.TrimSuffix(baseURL, "/")
	}

	// Check if URL already contains /v1 - don't add another one
	if strings.Contains(baseURL, "/v1") {
		return baseURL
	}

	// Parse the URL to analyze its structure
	if shouldAppendV1(baseURL) {
		return baseURL + "/v1"
	}

	return baseURL
}

// shouldAppendV1 determines if /v1 should be appended based on URL structure
func shouldAppendV1(baseURL string) bool {
	// For standard OpenAI API format (api.openai.com), we should append /v1
	if strings.Contains(strings.ToLower(baseURL), "api.openai.com") {
		return true
	}

	// Extract the path part after the domain
	parts := strings.Split(baseURL, "/")
	if len(parts) < 4 { // protocol://domain only, no path
		return true // Simple domain, likely needs /v1
	}

	// Count meaningful path segments (ignore empty strings)
	pathSegments := 0
	for i := 3; i < len(parts); i++ { // Start from index 3 to skip protocol://domain
		if parts[i] != "" {
			pathSegments++
		}
	}

	// If URL has 2+ path segments (like /api/openrouter), it's likely a proxy service
	// that has its own routing and doesn't need /v1
	if pathSegments >= 2 {
		return false
	}

	// Single path segment or simple domain - likely needs /v1
	return true
}

// GetFinalEndpointURL returns the complete endpoint URL for chat completions
func (c *OpenAIClient) GetFinalEndpointURL() string {
	if c == nil || c.baseURL == "" {
		return "https://api.openai.com/v1/chat/completions"
	}

	baseURL := constructBaseURL(c.baseURL)
	return baseURL + "/chat/completions"
}
