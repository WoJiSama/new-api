package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const relayFailureRateLimitMark = "RFL"
const relayFailureRecoveryProbeMark = "RFLP"
const relayFailureStorageTimeout = 2 * time.Second

type relayFailureWindow struct {
	mu          sync.Mutex
	events      map[string][]time.Time
	probeExpiry map[string]time.Time
}

var inMemoryRelayFailureWindow relayFailureWindow

// RelayFailureRateLimit temporarily rejects a token after it repeatedly
// receives a server-side failure. It deliberately ignores client-side 4xx
// errors so normal traffic remains unaffected.
func RelayFailureRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !setting.RelayFailureRateLimitEnabled || setting.RelayFailureRateLimitCount < 1 || setting.RelayFailureRateLimitDurationSeconds < 1 {
			c.Next()
			return
		}

		key, ok := relayFailureRateLimitKey(c)
		if !ok {
			c.Next()
			return
		}
		duration := time.Duration(setting.RelayFailureRateLimitDurationSeconds) * time.Second

		limited, retryAfter, err := relayFailureLimitReached(c, key, setting.RelayFailureRateLimitCount, duration)
		if err != nil {
			// Redis availability must not turn into an API outage. The in-memory
			// fallback still limits a single-process deployment.
			logger.LogError(c.Request.Context(), fmt.Sprintf("relay failure limiter check failed: %v", err))
			limited, retryAfter = inMemoryRelayFailureLimitReached(key, setting.RelayFailureRateLimitCount, duration)
		}
		if limited {
			// A token-wide failure window must not prevent recovery through a
			// different healthy channel. Admit one controlled probe, clear this
			// request's affinity, and let Distribute choose afresh. If that probe
			// also fails, the normal cooldown still protects every channel from a
			// client retry storm.
			probeAllowed, probeErr := takeRelayFailureRecoveryProbe(c, key, duration)
			if probeErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("relay failure limiter recovery probe failed: %v", probeErr))
				probeAllowed = inMemoryTakeRelayFailureRecoveryProbe(key, duration)
			}
			if probeAllowed {
				common.SetContextKey(c, constant.ContextKeyRelayFailureRecoveryProbe, true)
				service.ClearCurrentChannelAffinityCache(c)
				c.Next()
				finishRelayFailureRateLimit(c, key, duration)
				return
			}
			retryAfterSeconds := int64(retryAfter.Seconds())
			if retryAfterSeconds < 1 {
				retryAfterSeconds = 1
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "upstream temporarily unavailable; retry after the indicated delay")
			return
		}

		c.Next()
		finishRelayFailureRateLimit(c, key, duration)
	}
}

func finishRelayFailureRateLimit(c *gin.Context, key string, duration time.Duration) {
	// Errors emitted before an upstream channel was selected (for example no
	// eligible channel or local daily-quota selection failure) are not evidence
	// that a dispatched upstream is unavailable. They must not poison a token's
	// relay-failure window.
	if len(c.GetStringSlice("use_channel")) == 0 {
		return
	}
	if c.Writer.Status() < http.StatusInternalServerError {
		if err := clearRelayFailures(key); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("relay failure limiter clear failed: %v", err))
			inMemoryClearRelayFailures(key)
		}
		return
	}
	if err := recordRelayFailure(key, setting.RelayFailureRateLimitCount, duration); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("relay failure limiter record failed: %v", err))
		inMemoryRecordRelayFailure(key, duration)
	}
}

func relayFailureRateLimitKey(c *gin.Context) (string, bool) {
	if tokenID := c.GetInt("token_id"); tokenID > 0 {
		return fmt.Sprintf("token:%d", tokenID), true
	}
	if userID := c.GetInt("id"); userID > 0 {
		return fmt.Sprintf("user:%d", userID), true
	}
	return "", false
}

func relayFailureRedisKey(identity string) string {
	return fmt.Sprintf("%s:%s:%s", redisRateLimitNamespace, relayFailureRateLimitMark, identity)
}

func relayFailureRecoveryProbeRedisKey(identity string) string {
	return fmt.Sprintf("%s:%s:%s", redisRateLimitNamespace, relayFailureRecoveryProbeMark, identity)
}

func relayFailureLimitReached(c *gin.Context, identity string, limit int, duration time.Duration) (bool, time.Duration, error) {
	if !common.RedisEnabled || common.RDB == nil {
		limited, retryAfter := inMemoryRelayFailureLimitReached(identity, limit, duration)
		return limited, retryAfter, nil
	}

	key := relayFailureRedisKey(identity)
	count, err := common.RDB.Get(c.Request.Context(), key).Int()
	if err != nil {
		if err == redis.Nil {
			return false, 0, nil
		}
		return false, 0, err
	}
	if count < limit {
		return false, 0, nil
	}
	ttl, err := common.RDB.TTL(c.Request.Context(), key).Result()
	if err != nil {
		return false, 0, err
	}
	if ttl <= 0 {
		ttl = duration
	}
	return true, ttl, nil
}

func recordRelayFailure(identity string, limit int, duration time.Duration) error {
	if !common.RedisEnabled || common.RDB == nil {
		inMemoryRecordRelayFailure(identity, duration)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayFailureStorageTimeout)
	defer cancel()
	_, _, _, err := redisFixedWindowTake(ctx, relayFailureRedisKey(identity), limit, int64(duration.Seconds()))
	return err
}

func takeRelayFailureRecoveryProbe(c *gin.Context, identity string, duration time.Duration) (bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return inMemoryTakeRelayFailureRecoveryProbe(identity, duration), nil
	}
	return common.RDB.SetNX(c.Request.Context(), relayFailureRecoveryProbeRedisKey(identity), "1", duration).Result()
}

func clearRelayFailures(identity string) error {
	if !common.RedisEnabled || common.RDB == nil {
		inMemoryClearRelayFailures(identity)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayFailureStorageTimeout)
	defer cancel()
	return common.RDB.Del(ctx, relayFailureRedisKey(identity), relayFailureRecoveryProbeRedisKey(identity)).Err()
}

func inMemoryRelayFailureLimitReached(identity string, limit int, duration time.Duration) (bool, time.Duration) {
	inMemoryRelayFailureWindow.mu.Lock()
	defer inMemoryRelayFailureWindow.mu.Unlock()
	events := pruneRelayFailureEvents(inMemoryRelayFailureWindow.events[identity], duration)
	if len(events) == 0 {
		delete(inMemoryRelayFailureWindow.events, identity)
	} else {
		if inMemoryRelayFailureWindow.events == nil {
			inMemoryRelayFailureWindow.events = make(map[string][]time.Time)
		}
		inMemoryRelayFailureWindow.events[identity] = events
	}
	if len(events) < limit {
		return false, 0
	}
	return true, time.Until(events[0].Add(duration))
}

func inMemoryRecordRelayFailure(identity string, duration time.Duration) {
	inMemoryRelayFailureWindow.mu.Lock()
	defer inMemoryRelayFailureWindow.mu.Unlock()
	if inMemoryRelayFailureWindow.events == nil {
		inMemoryRelayFailureWindow.events = make(map[string][]time.Time)
	}
	events := pruneRelayFailureEvents(inMemoryRelayFailureWindow.events[identity], duration)
	inMemoryRelayFailureWindow.events[identity] = append(events, time.Now())
}

func inMemoryTakeRelayFailureRecoveryProbe(identity string, duration time.Duration) bool {
	inMemoryRelayFailureWindow.mu.Lock()
	defer inMemoryRelayFailureWindow.mu.Unlock()
	if inMemoryRelayFailureWindow.probeExpiry == nil {
		inMemoryRelayFailureWindow.probeExpiry = make(map[string]time.Time)
	}
	if expiry := inMemoryRelayFailureWindow.probeExpiry[identity]; expiry.After(time.Now()) {
		return false
	}
	inMemoryRelayFailureWindow.probeExpiry[identity] = time.Now().Add(duration)
	return true
}

func inMemoryClearRelayFailures(identity string) {
	inMemoryRelayFailureWindow.mu.Lock()
	defer inMemoryRelayFailureWindow.mu.Unlock()
	delete(inMemoryRelayFailureWindow.events, identity)
	delete(inMemoryRelayFailureWindow.probeExpiry, identity)
}

func pruneRelayFailureEvents(events []time.Time, duration time.Duration) []time.Time {
	cutoff := time.Now().Add(-duration)
	first := 0
	for first < len(events) && !events[first].After(cutoff) {
		first++
	}
	return events[first:]
}
