package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const relayFailureRateLimitMark = "RFL"

type relayFailureWindow struct {
	mu     sync.Mutex
	events map[string][]time.Time
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
			retryAfterSeconds := int64(retryAfter.Seconds())
			if retryAfterSeconds < 1 {
				retryAfterSeconds = 1
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "upstream temporarily unavailable; retry after the indicated delay")
			return
		}

		c.Next()
		if c.Writer.Status() < http.StatusInternalServerError {
			return
		}

		if err := recordRelayFailure(c, key, setting.RelayFailureRateLimitCount, duration); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("relay failure limiter record failed: %v", err))
			inMemoryRecordRelayFailure(key, duration)
		}
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

func recordRelayFailure(c *gin.Context, identity string, limit int, duration time.Duration) error {
	if !common.RedisEnabled || common.RDB == nil {
		inMemoryRecordRelayFailure(identity, duration)
		return nil
	}
	_, _, _, err := redisFixedWindowTake(c.Request.Context(), relayFailureRedisKey(identity), limit, int64(duration.Seconds()))
	return err
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

func pruneRelayFailureEvents(events []time.Time, duration time.Duration) []time.Time {
	cutoff := time.Now().Add(-duration)
	first := 0
	for first < len(events) && !events[first].After(cutoff) {
		first++
	}
	return events[first:]
}
