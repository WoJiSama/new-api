package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-redis/redis/v8"
)

const channelRPMWindow = time.Minute

var channelRPMNow = time.Now

const reserveChannelRPMScript = `
local now = tonumber(ARGV[1])
local cutoff = now - tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
local count = redis.call('ZCARD', KEYS[1])
if tonumber(ARGV[3]) == 1 and count >= tonumber(ARGV[4]) then
  return {0, count}
end
local sequence = redis.call('INCR', KEYS[2])
redis.call('PEXPIRE', KEYS[2], tonumber(ARGV[2]) + 1000)
redis.call('ZADD', KEYS[1], now, ARGV[1] .. ':' .. sequence)
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]) + 1000)
return {1, count + 1}
`

const currentChannelRPMScript = `
local cutoff = tonumber(ARGV[1]) - tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
return redis.call('ZCARD', KEYS[1])
`

func channelRPMRedisKeys(channelID int) []string {
	keyPrefix := fmt.Sprintf("channel:rpm:v1:{%d}", channelID)
	return []string{keyPrefix + ":events", keyPrefix + ":sequence"}
}

func redisInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer reply type %T", value)
	}
}

func ReserveChannelRPMForAttempt(ctx context.Context, channel *model.Channel, enforceSamePriorityLimit bool) (bool, error) {
	if channel == nil || channel.Id <= 0 {
		return false, fmt.Errorf("invalid channel for RPM reservation")
	}

	limit := channel.GetOtherSettings().SamePriorityRetryRPMLimit
	if enforceSamePriorityLimit && limit <= 0 {
		return false, nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		return !enforceSamePriorityLimit, nil
	}
	enforceLimit := 0
	if enforceSamePriorityLimit {
		enforceLimit = 1
	}

	values, err := common.RDB.Eval(
		ctx,
		reserveChannelRPMScript,
		channelRPMRedisKeys(channel.Id),
		channelRPMNow().UnixMilli(),
		channelRPMWindow.Milliseconds(),
		enforceLimit,
		limit,
	).Slice()
	if err != nil {
		common.SysError(fmt.Sprintf("failed to reserve channel RPM: channel_id=%d, error=%v", channel.Id, err))
		return !enforceSamePriorityLimit, nil
	}
	if len(values) != 2 {
		return false, fmt.Errorf("unexpected channel RPM reservation reply length %d", len(values))
	}
	allowed, err := redisInteger(values[0])
	if err != nil {
		return false, err
	}
	return allowed == 1, nil
}

func PopulateChannelRPM(ctx context.Context, channels []*model.Channel) error {
	for _, channel := range channels {
		if channel != nil {
			channel.CurrentRPM = 0
		}
	}
	if len(channels) == 0 || !common.RedisEnabled || common.RDB == nil {
		return nil
	}

	now := channelRPMNow().UnixMilli()
	pipeline := common.RDB.Pipeline()
	commands := make([]*redis.Cmd, len(channels))
	for index, channel := range channels {
		if channel == nil || channel.Id <= 0 {
			continue
		}
		commands[index] = pipeline.Eval(ctx, currentChannelRPMScript, channelRPMRedisKeys(channel.Id)[:1], now, channelRPMWindow.Milliseconds())
	}
	if _, err := pipeline.Exec(ctx); err != nil {
		return err
	}
	for index, command := range commands {
		if command == nil {
			continue
		}
		value, err := command.Result()
		if err != nil {
			return err
		}
		count, err := redisInteger(value)
		if err != nil {
			return err
		}
		channels[index].CurrentRPM = count
	}
	return nil
}
