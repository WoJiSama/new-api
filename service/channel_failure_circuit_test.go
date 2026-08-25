package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelFailureCircuitSkipsRecentlyFailedUpstream(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	ctx := context.Background()
	const channelID = 731
	upstreamError := types.NewErrorWithStatusCode(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)
	OpenChannelFailureCircuit(ctx, channelID, upstreamError)
	assert.True(t, IsChannelFailureCircuitOpen(ctx, channelID))

	redisServer.FastForward(channelFailureCircuitCooldown)
	assert.False(t, IsChannelFailureCircuitOpen(ctx, channelID))
}

func TestChannelFailureCircuitDoesNotOpenForClientErrors(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	OpenChannelFailureCircuit(
		context.Background(),
		732,
		types.NewErrorWithStatusCode(errors.New("invalid request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest),
	)
	assert.False(t, IsChannelFailureCircuitOpen(context.Background(), 732))
}
