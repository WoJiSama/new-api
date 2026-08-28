package relay

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// responsesViaChatCompletions keeps the public Responses API while sending a
// converted Chat Completions request to channels that do not implement
// /v1/responses natively.
func responsesViaChatCompletions(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request *dto.OpenAIResponsesRequest) *types.NewAPIError {
	result, err := service.ConvertRequestVia(c, info, request, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return types.NewError(fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if info.ChannelSetting.SystemPrompt != "" {
		applySystemPromptIfNeeded(c, info, chatRequest)
	}
	savedMode, savedPath, savedFormat, savedStream := info.RelayMode, info.RequestURLPath, info.RelayFormat, info.IsStream
	defer func() {
		info.RelayMode, info.RequestURLPath, info.RelayFormat, info.IsStream = savedMode, savedPath, savedFormat, savedStream
	}()
	info.RelayMode = relayconstant.RelayModeChatCompletions
	info.RequestURLPath = "/v1/chat/completions"
	info.RelayFormat = types.RelayFormatOpenAI
	// This header is specific to the native Codex Responses endpoint. It must
	// not be forwarded when the request is converted to Chat Completions.
	c.Set("responses_to_chat_completions", true)
	converted, err := adaptor.ConvertOpenAIRequest(c, info, chatRequest)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err := common.Marshal(converted)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()

	info.RelayFormat = types.RelayFormatOpenAI
	if request.Stream != nil {
		info.IsStream = *request.Stream
	}
	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return types.NewOpenAIError(fmt.Errorf("upstream returned nil response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	httpResp := resp.(*http.Response)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return service.RelayErrorHandler(c.Request.Context(), httpResp, false)
	}
	if info.IsStream {
		usage, apiErr := openaichannel.OaiChatToResponsesStreamHandler(c, info, httpResp)
		if apiErr == nil {
			service.PostTextConsumeQuota(c, info, usage, nil)
		}
		return apiErr
	}
	usage, apiErr := openaichannel.OaiChatToResponsesHandler(c, info, httpResp)
	if apiErr == nil {
		service.PostTextConsumeQuota(c, info, usage, nil)
	}
	return apiErr
}
