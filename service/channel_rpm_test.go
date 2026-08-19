package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReserveChannelRPMForAttemptEnforcesSamePriorityLimitAtomically(t *testing.T) {
	redisServer := miniredis.RunT(t)
	originalRedisEnabled := common.RedisEnabled
	originalRedisClient := common.RDB
	originalNow := channelRPMNow
	common.RedisEnabled = true
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RDB = redisClient
	now := time.Date(2026, time.August, 11, 21, 0, 0, 0, time.UTC)
	channelRPMNow = func() time.Time { return now }
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRedisClient
		channelRPMNow = originalNow
	})

	channel := &model.Channel{Id: 92010, OtherSettings: `{"same_priority_retry_rpm_limit":3}`}
	for range 3 {
		reserved, err := ReserveChannelRPMForAttempt(context.Background(), channel, false)
		require.NoError(t, err)
		assert.True(t, reserved)
	}
	channels := []*model.Channel{channel}
	require.NoError(t, PopulateChannelRPM(context.Background(), channels))
	assert.Equal(t, int64(3), channel.CurrentRPM)

	reserved, err := ReserveChannelRPMForAttempt(context.Background(), channel, true)
	require.NoError(t, err)
	assert.False(t, reserved)

	now = now.Add(channelRPMWindow)
	reserved, err = ReserveChannelRPMForAttempt(context.Background(), channel, true)
	require.NoError(t, err)
	assert.True(t, reserved)
}
