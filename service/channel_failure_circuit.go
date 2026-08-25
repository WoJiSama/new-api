package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const channelFailureCircuitCooldown = time.Minute

func channelFailureCircuitKey(channelID int) string {
	return fmt.Sprintf("relay:channel-circuit:v1:%d", channelID)
}

// OpenChannelFailureCircuit prevents a channel that just returned an upstream
// overload or server error from being selected again during the cooldown.
func OpenChannelFailureCircuit(ctx context.Context, channelID int, relayErr *types.NewAPIError) {
	if channelID <= 0 || !shouldOpenChannelFailureCircuit(relayErr) || !common.RedisEnabled || common.RDB == nil {
		return
	}
	if err := common.RDB.SetEX(ctx, channelFailureCircuitKey(channelID), "1", channelFailureCircuitCooldown).Err(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to open channel failure circuit: channel_id=%d, error=%v", channelID, err))
	}
}

func IsChannelFailureCircuitOpen(ctx context.Context, channelID int) bool {
	if channelID <= 0 || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	open, err := common.RDB.Exists(ctx, channelFailureCircuitKey(channelID)).Result()
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to check channel failure circuit: channel_id=%d, error=%v", channelID, err))
		return false
	}
	return open > 0
}

func shouldOpenChannelFailureCircuit(relayErr *types.NewAPIError) bool {
	if relayErr == nil {
		return false
	}
	return relayErr.StatusCode == http.StatusTooManyRequests || relayErr.StatusCode >= http.StatusInternalServerError
}
