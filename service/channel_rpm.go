package service

import (
	"context"
	"errors"
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
local current = count + 1
local peak = tonumber(redis.call('GET', KEYS[3]) or '0')
if current > peak then
  redis.call('PSETEX', KEYS[3], tonumber(ARGV[5]), current)
elseif redis.call('PTTL', KEYS[3]) < 0 then
  redis.call('PSETEX', KEYS[3], tonumber(ARGV[5]), peak)
end
return {1, current}
`

const currentChannelRPMScript = `
local cutoff = tonumber(ARGV[1]) - tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
return redis.call('ZCARD', KEYS[1])
`

func channelRPMRedisKeys(channelID int, now time.Time) []string {
	keyPrefix := fmt.Sprintf("channel:rpm:v1:{%d}", channelID)
	return []string{
		keyPrefix + ":events",
		keyPrefix + ":sequence",
		keyPrefix + ":daily-peak:" + now.Format("2006-01-02"),
	}
}

func channelRPMDailyPeakTTL(now time.Time) time.Duration {
	tomorrow := time.Date(
		now.Year(), now.Month(), now.Day()+1,
		0, 0, 0, 0, now.Location(),
	)
	ttl := tomorrow.Sub(now)
	if ttl < time.Millisecond {
		return time.Millisecond
	}
	return ttl
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

	now := channelRPMNow()
	values, err := common.RDB.Eval(
		ctx,
		reserveChannelRPMScript,
		channelRPMRedisKeys(channel.Id, now),
		now.UnixMilli(),
		channelRPMWindow.Milliseconds(),
		enforceLimit,
		limit,
		channelRPMDailyPeakTTL(now).Milliseconds(),
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
			channel.DailyPeakRPM = 0
		}
	}
	if len(channels) == 0 || !common.RedisEnabled || common.RDB == nil {
		return nil
	}

	now := channelRPMNow()
	pipeline := common.RDB.Pipeline()
	currentCommands := make([]*redis.Cmd, len(channels))
	peakCommands := make([]*redis.StringCmd, len(channels))
	for index, channel := range channels {
		if channel == nil || channel.Id <= 0 {
			continue
		}
		keys := channelRPMRedisKeys(channel.Id, now)
		currentCommands[index] = pipeline.Eval(
			ctx,
			currentChannelRPMScript,
			keys[:1],
			now.UnixMilli(),
			channelRPMWindow.Milliseconds(),
		)
		peakCommands[index] = pipeline.Get(ctx, keys[2])
	}
	if _, err := pipeline.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	for index, command := range currentCommands {
		if command == nil || peakCommands[index] == nil {
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

		peakValue, err := peakCommands[index].Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if err == nil {
			peak, err := redisInteger(peakValue)
			if err != nil {
				return err
			}
			channels[index].DailyPeakRPM = peak
		}
	}
	return nil
}
