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
	assert.Equal(t, int64(3), channel.DailyPeakRPM)

	reserved, err := ReserveChannelRPMForAttempt(context.Background(), channel, true)
	require.NoError(t, err)
	assert.False(t, reserved)

	now = now.Add(channelRPMWindow)
	require.NoError(t, PopulateChannelRPM(context.Background(), channels))
	assert.Zero(t, channel.CurrentRPM)
	assert.Equal(t, int64(3), channel.DailyPeakRPM)

	reserved, err = ReserveChannelRPMForAttempt(context.Background(), channel, true)
	require.NoError(t, err)
	assert.True(t, reserved)
	require.NoError(t, PopulateChannelRPM(context.Background(), channels))
	assert.Equal(t, int64(1), channel.CurrentRPM)
	assert.Equal(t, int64(3), channel.DailyPeakRPM)

	now = time.Date(2026, time.August, 12, 0, 1, 0, 0, time.UTC)
	reserved, err = ReserveChannelRPMForAttempt(context.Background(), channel, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	require.NoError(t, PopulateChannelRPM(context.Background(), channels))
	assert.Equal(t, int64(1), channel.CurrentRPM)
	assert.Equal(t, int64(1), channel.DailyPeakRPM)
}

func TestPopulateChannelRPMAllowsChannelsWithoutPeakData(t *testing.T) {
	redisServer := miniredis.RunT(t)
	originalRedisEnabled := common.RedisEnabled
	originalRedisClient := common.RDB
	originalNow := channelRPMNow
	common.RedisEnabled = true
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RDB = redisClient
	channelRPMNow = func() time.Time {
		return time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC)
	}
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRedisClient
		channelRPMNow = originalNow
	})

	activeChannel := &model.Channel{Id: 92011}
	inactiveChannel := &model.Channel{Id: 92012}
	reserved, err := ReserveChannelRPMForAttempt(context.Background(), activeChannel, false)
	require.NoError(t, err)
	require.True(t, reserved)

	require.NoError(t, PopulateChannelRPM(context.Background(), []*model.Channel{activeChannel, inactiveChannel}))
	assert.Equal(t, int64(1), activeChannel.CurrentRPM)
	assert.Equal(t, int64(1), activeChannel.DailyPeakRPM)
	assert.Zero(t, inactiveChannel.CurrentRPM)
	assert.Zero(t, inactiveChannel.DailyPeakRPM)
}
