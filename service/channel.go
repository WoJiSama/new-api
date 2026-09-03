package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string, sourceErrors ...*types.NewAPIError) {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(reason)))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	updateOptions := recoveryRetryOptions(channelError.ChannelId, firstSourceError(sourceErrors))

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason, updateOptions...)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
}

// DisableChannelForDailyQuota marks an enabled channel as auto-disabled when
// its configured daily budget is exhausted. Daily quota exhaustion is a local
// scheduling condition, so it must not depend on the upstream-error keyword or
// status-code rules used by DisableChannel.
func DisableChannelForDailyQuota(channel *model.Channel) {
	if channel == nil || !channel.GetAutoBan() {
		return
	}
	if channel.Status != common.ChannelStatusEnabled {
		return
	}
	reason := fmt.Sprintf("渠道 #%d 今日额度不足", channel.Id)
	if model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, reason) {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channel.Name, channel.Id)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channel.Name, channel.Id, reason)
		NotifyRootUser(formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), subject, content)
	}
}

// RefreshChannelRecoveryRetry starts a fresh custom wait after a recovery test
// fails. When its error no longer matches a rule, clearing the deadline makes
// the next check naturally fall back to the global monitor interval.
func RefreshChannelRecoveryRetry(channelId int, sourceError *types.NewAPIError) {
	if err := model.UpdateChannelRecoveryRetryAfter(channelId, recoveryRetryOptions(channelId, sourceError)...); err != nil {
		common.SysLog(fmt.Sprintf("failed to refresh channel recovery retry: channel_id=%d, error=%v", channelId, err))
	}
}

func firstSourceError(sourceErrors []*types.NewAPIError) *types.NewAPIError {
	if len(sourceErrors) == 0 {
		return nil
	}
	return sourceErrors[0]
}

func recoveryRetryOptions(channelId int, sourceError *types.NewAPIError) []model.ChannelStatusUpdateOptions {
	if sourceError == nil {
		return nil
	}
	channel, err := model.GetChannelById(channelId, false)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to load channel recovery retry settings: channel_id=%d, error=%v", channelId, err))
		return nil
	}
	settings := channel.GetOtherSettings()
	rule, ok := settings.MatchRecoveryRetryRule(sourceError.StatusCode, sourceError.Error())
	if !ok {
		return nil
	}
	return []model.ChannelStatusUpdateOptions{{
		RetryAfter:          time.Now().Add(time.Duration(rule.RetryAfterMinutes) * time.Minute).Unix(),
		RetryIntervalMinute: rule.RetryAfterMinutes,
		RetryRuleStatusCode: rule.StatusCode,
		RetryRuleContains:   strings.TrimSpace(rule.ErrorContains),
	}}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
