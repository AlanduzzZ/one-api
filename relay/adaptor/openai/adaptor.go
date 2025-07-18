package openai

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/Laisky/errors/v2"
	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/adaptor/alibailian"
	"github.com/songquanpeng/one-api/relay/adaptor/baiduv2"
	"github.com/songquanpeng/one-api/relay/adaptor/doubao"
	"github.com/songquanpeng/one-api/relay/adaptor/geminiOpenaiCompatible"
	"github.com/songquanpeng/one-api/relay/adaptor/minimax"
	"github.com/songquanpeng/one-api/relay/adaptor/novita"
	"github.com/songquanpeng/one-api/relay/billing/ratio"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/relaymode"
)

type Adaptor struct {
	ChannelType int
}

func (a *Adaptor) Init(meta *meta.Meta) {
	a.ChannelType = meta.ChannelType
}

func (a *Adaptor) GetRequestURL(meta *meta.Meta) (string, error) {
	switch meta.ChannelType {
	case channeltype.Azure:
		defaultVersion := meta.Config.APIVersion

		// https://learn.microsoft.com/en-us/azure/ai-services/openai/how-to/reasoning?tabs=python#api--feature-support
		if strings.HasPrefix(meta.ActualModelName, "o1") ||
			strings.HasPrefix(meta.ActualModelName, "o3") {
			defaultVersion = "2024-12-01-preview"
		}

		if meta.Mode == relaymode.ImagesGenerations {
			// https://learn.microsoft.com/en-us/azure/ai-services/openai/dall-e-quickstart?tabs=dalle3%2Ccommand-line&pivots=rest-api
			// https://{resource_name}.openai.azure.com/openai/deployments/dall-e-3/images/generations?api-version=2024-03-01-preview
			fullRequestURL := fmt.Sprintf("%s/openai/deployments/%s/images/generations?api-version=%s", meta.BaseURL, meta.ActualModelName, defaultVersion)
			return fullRequestURL, nil
		}

		// https://learn.microsoft.com/en-us/azure/cognitive-services/openai/chatgpt-quickstart?pivots=rest-api&tabs=command-line#rest-api
		requestURL := strings.Split(meta.RequestURLPath, "?")[0]
		requestURL = fmt.Sprintf("%s?api-version=%s", requestURL, defaultVersion)
		task := strings.TrimPrefix(requestURL, "/v1/")
		model_ := meta.ActualModelName
		// https://github.com/songquanpeng/one-api/issues/2235
		// model_ = strings.Replace(model_, ".", "", -1)
		//https://github.com/songquanpeng/one-api/issues/1191
		// {your endpoint}/openai/deployments/{your azure_model}/chat/completions?api-version={api_version}
		requestURL = fmt.Sprintf("/openai/deployments/%s/%s", model_, task)
		return GetFullRequestURL(meta.BaseURL, requestURL, meta.ChannelType), nil
	case channeltype.Minimax:
		return minimax.GetRequestURL(meta)
	case channeltype.Doubao:
		return doubao.GetRequestURL(meta)
	case channeltype.Novita:
		return novita.GetRequestURL(meta)
	case channeltype.BaiduV2:
		return baiduv2.GetRequestURL(meta)
	case channeltype.AliBailian:
		return alibailian.GetRequestURL(meta)
	case channeltype.GeminiOpenAICompatible:
		return geminiOpenaiCompatible.GetRequestURL(meta)
	default:
		// Convert chat completions to responses API for OpenAI only
		// Skip conversion for models that only support ChatCompletion API
		if meta.Mode == relaymode.ChatCompletions &&
			meta.ChannelType == channeltype.OpenAI &&
			!IsModelsOnlySupportedByChatCompletionAPI(meta.ActualModelName) {
			responseAPIPath := "/v1/responses"
			return GetFullRequestURL(meta.BaseURL, responseAPIPath, meta.ChannelType), nil
		}
		return GetFullRequestURL(meta.BaseURL, meta.RequestURLPath, meta.ChannelType), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error {
	adaptor.SetupCommonRequestHeader(c, req, meta)
	if meta.ChannelType == channeltype.Azure {
		req.Header.Set("api-key", meta.APIKey)
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	if meta.ChannelType == channeltype.OpenRouter {
		req.Header.Set("HTTP-Referer", "https://github.com/Laisky/one-api")
		req.Header.Set("X-Title", "One API")
	}
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	meta := meta.GetByContext(c)

	// Handle direct Response API requests
	if relayMode == relaymode.ResponseAPI {
		// For direct Response API requests, the request should already be in the correct format
		// We don't need to convert it, just pass it through
		return request, nil
	}

	// Convert ChatCompletion requests to Response API format only for OpenAI
	// Skip conversion for models that only support ChatCompletion API
	if relayMode == relaymode.ChatCompletions &&
		meta.ChannelType == channeltype.OpenAI &&
		!IsModelsOnlySupportedByChatCompletionAPI(meta.ActualModelName) {
		// Apply existing transformations first
		if err := a.applyRequestTransformations(meta, request); err != nil {
			return nil, err
		}

		// Convert to Response API format
		responseAPIRequest := ConvertChatCompletionToResponseAPI(request)

		// Store the converted request in context to detect it later in DoResponse
		c.Set(ctxkey.ConvertedRequest, responseAPIRequest)

		return responseAPIRequest, nil
	}

	// Apply existing transformations for other modes
	if err := a.applyRequestTransformations(meta, request); err != nil {
		return nil, err
	}

	return request, nil
}

// applyRequestTransformations applies the existing request transformations
func (a *Adaptor) applyRequestTransformations(meta *meta.Meta, request *model.GeneralOpenAIRequest) error {
	switch meta.ChannelType {
	case channeltype.OpenRouter:
		includeReasoning := true
		request.IncludeReasoning = &includeReasoning
		if request.Provider == nil || request.Provider.Sort == "" &&
			config.OpenrouterProviderSort != "" {
			if request.Provider == nil {
				request.Provider = &model.RequestProvider{}
			}

			request.Provider.Sort = config.OpenrouterProviderSort
		}
	default:
	}

	if request.Stream && !config.EnforceIncludeUsage {
		logger.Warn(context.Background(),
			"please set ENFORCE_INCLUDE_USAGE=true to ensure accurate billing in stream mode")
	}

	if config.EnforceIncludeUsage && request.Stream {
		// always return usage in stream mode
		if request.StreamOptions == nil {
			request.StreamOptions = &model.StreamOptions{}
		}
		request.StreamOptions.IncludeUsage = true
	}

	// o1/o3/o4 do not support system prompt/max_tokens/temperature
	if strings.HasPrefix(meta.ActualModelName, "o") {
		temperature := float64(1)
		request.Temperature = &temperature // Only the default (1) value is supported
		request.MaxTokens = 0
		request.TopP = nil
		if request.ReasoningEffort == nil {
			effortHigh := "high"
			request.ReasoningEffort = &effortHigh
		}

		request.Messages = func(raw []model.Message) (filtered []model.Message) {
			for i := range raw {
				if raw[i].Role != "system" {
					filtered = append(filtered, raw[i])
				}
			}

			return
		}(request.Messages)
	} else {
		request.ReasoningEffort = nil
	}

	// web search do not support system prompt/max_tokens/temperature
	if strings.HasSuffix(meta.ActualModelName, "-search") {
		request.Temperature = nil
		request.TopP = nil
		request.PresencePenalty = nil
		request.N = nil
		request.FrequencyPenalty = nil
	}

	if request.Stream && !config.EnforceIncludeUsage &&
		strings.HasSuffix(request.Model, "-audio") {
		// TODO: Since it is not clear how to implement billing in stream mode,
		// it is temporarily not supported
		return errors.New("set ENFORCE_INCLUDE_USAGE=true to enable stream mode for gpt-4o-audio")
	}

	return nil
}

func (a *Adaptor) ConvertImageRequest(_ *gin.Context, request *model.ImageRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context,
	meta *meta.Meta,
	requestBody io.Reader) (*http.Response, error) {
	return adaptor.DoRequestHelper(a, c, meta, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context,
	resp *http.Response,
	meta *meta.Meta) (usage *model.Usage,
	err *model.ErrorWithStatusCode) {
	if meta.IsStream {
		var responseText string
		// Handle different streaming modes
		switch meta.Mode {
		case relaymode.ResponseAPI:
			// Direct Response API streaming - pass through without conversion
			err, responseText, usage = ResponseAPIDirectStreamHandler(c, resp, meta.Mode)
		default:
			// Check if we need to handle Response API streaming response for ChatCompletion
			if vi, ok := c.Get(ctxkey.ConvertedRequest); ok {
				if _, ok := vi.(*ResponseAPIRequest); ok {
					// This is a Response API streaming response that needs conversion
					err, responseText, usage = ResponseAPIStreamHandler(c, resp, meta.Mode)
				} else {
					// Regular streaming response
					err, responseText, usage = StreamHandler(c, resp, meta.Mode)
				}
			} else {
				// Regular streaming response
				err, responseText, usage = StreamHandler(c, resp, meta.Mode)
			}
		}

		if usage == nil || usage.TotalTokens == 0 {
			usage = ResponseText2Usage(responseText, meta.ActualModelName, meta.PromptTokens)
		}
		if usage.TotalTokens != 0 && usage.PromptTokens == 0 { // some channels don't return prompt tokens & completion tokens
			usage.PromptTokens = meta.PromptTokens
			usage.CompletionTokens = usage.TotalTokens - meta.PromptTokens
		}
	} else {
		switch meta.Mode {
		case relaymode.ImagesGenerations,
			relaymode.ImagesEdits:
			err, usage = ImageHandler(c, resp)
		// case relaymode.ImagesEdits:
		// err, usage = ImagesEditsHandler(c, resp)
		case relaymode.ResponseAPI:
			// For direct Response API requests, pass through the response directly
			// without conversion back to ChatCompletion format
			err, usage = ResponseAPIDirectHandler(c, resp, meta.PromptTokens, meta.ActualModelName)
		case relaymode.ChatCompletions:
			// Check if we need to convert Response API response back to ChatCompletion format
			if vi, ok := c.Get(ctxkey.ConvertedRequest); ok {
				if _, ok := vi.(*ResponseAPIRequest); ok {
					// This is a Response API response that needs conversion
					err, usage = ResponseAPIHandler(c, resp, meta.PromptTokens, meta.ActualModelName)
				} else {
					// Regular ChatCompletion request
					err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
				}
			} else {
				// Regular ChatCompletion request
				err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
			}
		default:
			err, usage = Handler(c, resp, meta.PromptTokens, meta.ActualModelName)
		}
	}

	// -------------------------------------
	// calculate web-search tool cost
	// -------------------------------------
	if usage != nil {
		searchContextSize := "medium"
		var req *model.GeneralOpenAIRequest
		if vi, ok := c.Get(ctxkey.ConvertedRequest); ok {
			if req, ok = vi.(*model.GeneralOpenAIRequest); ok {
				if req != nil &&
					req.WebSearchOptions != nil &&
					req.WebSearchOptions.SearchContextSize != nil {
					searchContextSize = *req.WebSearchOptions.SearchContextSize
				}

				switch {
				case strings.HasPrefix(meta.ActualModelName, "gpt-4o-search"):
					switch searchContextSize {
					case "low":
						usage.ToolsCost += int64(math.Ceil(30 / 1000 * ratio.QuotaPerUsd))
					case "medium":
						usage.ToolsCost += int64(math.Ceil(35 / 1000 * ratio.QuotaPerUsd))
					case "high":
						usage.ToolsCost += int64(math.Ceil(40 / 1000 * ratio.QuotaPerUsd))
					default:
						return nil, ErrorWrapper(
							errors.Errorf("invalid search context size %q", searchContextSize),
							"invalid search context size: "+searchContextSize,
							http.StatusBadRequest)
					}
				case strings.HasPrefix(meta.ActualModelName, "gpt-4o-mini-search"):
					switch searchContextSize {
					case "low":
						usage.ToolsCost += int64(math.Ceil(25 / 1000 * ratio.QuotaPerUsd))
					case "medium":
						usage.ToolsCost += int64(math.Ceil(27.5 / 1000 * ratio.QuotaPerUsd))
					case "high":
						usage.ToolsCost += int64(math.Ceil(30 / 1000 * ratio.QuotaPerUsd))
					default:
						return nil, ErrorWrapper(
							errors.Errorf("invalid search context size %q", searchContextSize),
							"invalid search context size: "+searchContextSize,
							http.StatusBadRequest)
					}
				}

				// -------------------------------------
				// calculate structured output cost
				// -------------------------------------
				// Structured output with json_schema incurs additional costs
				// Based on OpenAI's pricing, structured output typically has a multiplier applied
				if req.ResponseFormat != nil &&
					req.ResponseFormat.Type == "json_schema" &&
					req.ResponseFormat.JsonSchema != nil {
					// Apply structured output cost multiplier
					// For structured output, there's typically an additional cost based on completion tokens
					// Using a conservative estimate of 25% additional cost for structured output

					// get channel-specific pricing if available
					var channelModelRatio map[string]float64
					if channelModel, ok := c.Get(ctxkey.ChannelModel); ok {
						if channel, ok := channelModel.(*dbmodel.Channel); ok {
							channelModelRatio = channel.GetModelRatio()
						}
					}

					modelRatio := ratio.GetModelRatioWithChannel(meta.ActualModelName, meta.ChannelType, channelModelRatio)
					structuredOutputCost := int64(math.Ceil(float64(usage.CompletionTokens) * 0.25 * modelRatio))
					usage.ToolsCost += structuredOutputCost

					// Log structured output cost application for debugging
					logger.Debugf(c.Request.Context(), "Applied structured output cost: %d (completion tokens: %d, model: %s)",
						structuredOutputCost, usage.CompletionTokens, meta.ActualModelName)
				}
			}
		}

		// Also check the original request in case it wasn't converted
		if req == nil {
			if vi, ok := c.Get(ctxkey.RequestModel); ok {
				if req, ok = vi.(*model.GeneralOpenAIRequest); ok && req != nil {
					if req.ResponseFormat != nil &&
						req.ResponseFormat.Type == "json_schema" &&
						req.ResponseFormat.JsonSchema != nil {
						// Apply structured output cost multiplier

						// get channel-specific pricing if available
						var channelModelRatio map[string]float64
						if channelModel, ok := c.Get(ctxkey.ChannelModel); ok {
							if channel, ok := channelModel.(*dbmodel.Channel); ok {
								// Get from unified ModelConfigs only (after migration)
								channelModelRatio = channel.GetModelRatioFromConfigs()
							}
						}

						modelRatio := ratio.GetModelRatioWithChannel(meta.ActualModelName, meta.ChannelType, channelModelRatio)
						structuredOutputCost := int64(math.Ceil(float64(usage.CompletionTokens) * 0.25 * modelRatio))
						usage.ToolsCost += structuredOutputCost

						// Log structured output cost application for debugging
						logger.Debugf(c.Request.Context(), "Applied structured output cost from original request: %d (completion tokens: %d, model: %s)",
							structuredOutputCost, usage.CompletionTokens, meta.ActualModelName)
					}
				}
			}
		}
	}

	return
}

func (a *Adaptor) GetModelList() []string {
	return adaptor.GetModelListFromPricing(ModelRatios)
}

func (a *Adaptor) GetChannelName() string {
	channelName, _ := GetCompatibleChannelMeta(a.ChannelType)
	return channelName
}

// Pricing methods - OpenAI adapter manages its own model pricing
func (a *Adaptor) GetDefaultModelPricing() map[string]adaptor.ModelConfig {
	return ModelRatios
}

func (a *Adaptor) GetModelRatio(modelName string) float64 {
	pricing := a.GetDefaultModelPricing()
	if price, exists := pricing[modelName]; exists {
		return price.Ratio
	}
	// Fallback to global pricing for unknown models
	return ratio.GetModelRatio(modelName, a.ChannelType)
}

func (a *Adaptor) GetCompletionRatio(modelName string) float64 {
	pricing := a.GetDefaultModelPricing()
	if price, exists := pricing[modelName]; exists {
		return price.CompletionRatio
	}
	// Fallback to global pricing for unknown models
	return ratio.GetCompletionRatio(modelName, a.ChannelType)
}
