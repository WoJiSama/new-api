package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixedChannelDailyQuotaExhaustionStopsSelection(t *testing.T) {
	channel := &model.Channel{Id: 4}
	newAPIError := fixedChannelDailyQuotaExhaustionError(&relaycommon.RelayInfo{}, channel)

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIError))
}

func TestDynamicChannelDailyQuotaExhaustionCanContinueSelection(t *testing.T) {
	channel := &model.Channel{Id: 4}
	relayInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	assert.Nil(t, fixedChannelDailyQuotaExhaustionError(relayInfo, channel))
}
