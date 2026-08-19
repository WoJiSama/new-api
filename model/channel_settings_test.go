package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsRejectsInvalidHTTPTransport(t *testing.T) {
	tests := []struct {
		name    string
		setting dto.ChannelSettings
		wantErr string
	}{
		{
			name:    "auto with shards is valid",
			setting: dto.ChannelSettings{HTTPProtocol: "auto", HTTP2ConnectionShards: 4},
		},
		{
			name:    "http1 with shards greater than one rejected",
			setting: dto.ChannelSettings{HTTPProtocol: "http1", HTTP2ConnectionShards: 2},
			wantErr: "http2_connection_shards",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{}
			channel.SetSetting(tt.setting)
			err := channel.ValidateSettings()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestChannelValidateSettingsRejectsInvalidRecoveryRetryRules(t *testing.T) {
	tests := []struct {
		name    string
		rule    dto.ChannelRecoveryRetryRule
		wantErr string
	}{
		{
			name:    "invalid status code",
			rule:    dto.ChannelRecoveryRetryRule{StatusCode: 99, RetryAfterMinutes: 1},
			wantErr: "status_code",
		},
		{
			name:    "non-positive interval",
			rule:    dto.ChannelRecoveryRetryRule{StatusCode: 429, RetryAfterMinutes: 0},
			wantErr: "retry_after_minutes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{}
			channel.SetOtherSettings(dto.ChannelOtherSettings{
				ChannelRecoveryRetryRules: []dto.ChannelRecoveryRetryRule{tt.rule},
			})
			err := channel.ValidateSettings()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestChannelRecoveryRetryRulesUseFirstMatchingRule(t *testing.T) {
	settings := dto.ChannelOtherSettings{ChannelRecoveryRetryRules: []dto.ChannelRecoveryRetryRule{
		{StatusCode: 429, ErrorContains: "weekly_limit", RetryAfterMinutes: 180},
		{StatusCode: 429, RetryAfterMinutes: 10},
		{RetryAfterMinutes: 5},
	}}

	rule, ok := settings.MatchRecoveryRetryRule(429, "WEEKLY_LIMIT_EXCEEDED")
	require.True(t, ok)
	assert.Equal(t, int64(180), rule.RetryAfterMinutes)

	rule, ok = settings.MatchRecoveryRetryRule(429, "rate limit")
	require.True(t, ok)
	assert.Equal(t, int64(10), rule.RetryAfterMinutes)

	rule, ok = settings.MatchRecoveryRetryRule(500, "server error")
	require.True(t, ok)
	assert.Equal(t, int64(5), rule.RetryAfterMinutes)
}

func TestUpdateChannelStatusPersistsAndClearsRecoveryDeadline(t *testing.T) {
	truncateTables(t)
	channel := &Channel{
		Id:     92001,
		Name:   "recovery-deadline",
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(channel).Error)

	retryAfter := int64(1_700_000_600)
	require.True(t, UpdateChannelStatus(
		channel.Id,
		"",
		common.ChannelStatusAutoDisabled,
		"429 WEEKLY_LIMIT_EXCEEDED",
		ChannelStatusUpdateOptions{
			RetryAfter:          retryAfter,
			RetryIntervalMinute: 10,
			RetryRuleStatusCode: 429,
			RetryRuleContains:   "WEEKLY_LIMIT_EXCEEDED",
		},
	))

	disabled, err := GetChannelById(channel.Id, false)
	require.NoError(t, err)
	assert.Equal(t, retryAfter, disabled.RecoveryRetryAfter())

	refreshedRetryAfter := retryAfter + 600
	require.NoError(t, UpdateChannelRecoveryRetryAfter(channel.Id, ChannelStatusUpdateOptions{
		RetryAfter:          refreshedRetryAfter,
		RetryIntervalMinute: 10,
		RetryRuleStatusCode: 429,
	}))
	refreshed, err := GetChannelById(channel.Id, false)
	require.NoError(t, err)
	assert.Equal(t, refreshedRetryAfter, refreshed.RecoveryRetryAfter())

	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, ""))
	enabled, err := GetChannelById(channel.Id, false)
	require.NoError(t, err)
	assert.Zero(t, enabled.RecoveryRetryAfter())
}

func TestAdvancedCustomChannelRequiresModelListRouteOnlyWhenUpdateChecksEnabled(t *testing.T) {
	inferenceRoute := dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
		Converter:    "none",
	}

	tests := []struct {
		name          string
		checksEnabled bool
		routes        []dto.AdvancedCustomRoute
		wantErr       string
	}{
		{
			name:   "legacy channel without discovery route remains valid",
			routes: []dto.AdvancedCustomRoute{inferenceRoute},
		},
		{
			name:          "enabled checks require discovery route",
			checksEnabled: true,
			routes:        []dto.AdvancedCustomRoute{inferenceRoute},
			wantErr:       dto.AdvancedCustomModelListPath,
		},
		{
			name:          "enabled checks accept discovery route",
			checksEnabled: true,
			routes: []dto.AdvancedCustomRoute{
				inferenceRoute,
				{
					IncomingPath: dto.AdvancedCustomModelListPath,
					UpstreamPath: dto.AdvancedCustomModelListPath,
					Converter:    "none",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{Type: constant.ChannelTypeAdvancedCustom}
			channel.SetOtherSettings(dto.ChannelOtherSettings{
				UpstreamModelUpdateCheckEnabled: tt.checksEnabled,
				AdvancedCustom: &dto.AdvancedCustomConfig{
					Routes: tt.routes,
				},
			})

			err := channel.ValidateSettings()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
