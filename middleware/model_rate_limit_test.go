package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRedisRateLimitUsesUTCRegardlessOfLocalTimezone(t *testing.T) {
	redisServer, redisClient := useRateLimitMiniRedis(t)
	previousLocation := time.Local
	time.Local = time.FixedZone("test-utc-plus-eight", 8*60*60)
	t.Cleanup(func() { time.Local = previousLocation })

	ctx := context.Background()
	recordKey := "rateLimit:model-utc-record"
	recordRedisRequest(ctx, redisClient, recordKey, 2)
	recorded, err := redisClient.LIndex(ctx, recordKey, 0).Result()
	require.NoError(t, err)
	recordedAt, err := time.Parse(modelRateLimitTimeFormat, recorded)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), recordedAt, 2*time.Second)

	checkKey := "rateLimit:model-utc-check"
	withinWindow := time.Now().UTC().Add(-30 * time.Second).Format(modelRateLimitTimeFormat)
	_, err = redisServer.Push(checkKey, withinWindow, withinWindow)
	require.NoError(t, err)
	allowed, err := checkRedisRateLimit(ctx, redisClient, checkKey, 2, 60)
	require.NoError(t, err)
	assert.False(t, allowed, "an existing UTC timestamp inside the window must remain limited on a non-UTC host")
}

func TestRedisModelRateLimitUsesConfiguredFixedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)

	router := gin.New()
	router.GET(
		"/limited",
		func(c *gin.Context) { c.Set("id", 42) },
		redisRateLimitHandler(60, 2, 100),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/limited", nil))
		return recorder
	}

	assert.Equal(t, http.StatusNoContent, request().Code)
	assert.Equal(t, http.StatusNoContent, request().Code)
	limited := request()
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	assert.Equal(t, "60", limited.Header().Get("Retry-After"))
}

func TestRelayFailureRateLimitAllowsOneFreshRecoveryProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)
	previousEnabled := setting.RelayFailureRateLimitEnabled
	previousCount := setting.RelayFailureRateLimitCount
	previousDuration := setting.RelayFailureRateLimitDurationSeconds
	setting.RelayFailureRateLimitEnabled = true
	setting.RelayFailureRateLimitCount = 2
	setting.RelayFailureRateLimitDurationSeconds = 30
	t.Cleanup(func() {
		setting.RelayFailureRateLimitEnabled = previousEnabled
		setting.RelayFailureRateLimitCount = previousCount
		setting.RelayFailureRateLimitDurationSeconds = previousDuration
	})

	router := gin.New()
	router.GET(
		"/relay",
		func(c *gin.Context) { c.Set("token_id", 7) },
		RelayFailureRateLimit(),
		func(c *gin.Context) {
			c.Set("use_channel", []string{"9"})
			if common.GetContextKeyBool(c, constant.ContextKeyRelayFailureRecoveryProbe) {
				requestCtx, cancel := context.WithCancel(c.Request.Context())
				c.Request = c.Request.WithContext(requestCtx)
				cancel()
				c.Status(http.StatusNoContent)
				return
			}
			c.Status(http.StatusServiceUnavailable)
		},
	)

	request := func(tokenID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/relay", nil)
		router.ServeHTTP(recorder, request)
		return recorder
	}

	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
	// This used to be rejected locally before Distribute could try another
	// channel. It is now a single recovery probe and resets the failure window
	// after the fresh selection succeeds.
	assert.Equal(t, http.StatusNoContent, request("7").Code)
	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
}

func TestRelayFailureRateLimitIgnoresLocalSelectionFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)
	previousEnabled := setting.RelayFailureRateLimitEnabled
	previousCount := setting.RelayFailureRateLimitCount
	previousDuration := setting.RelayFailureRateLimitDurationSeconds
	setting.RelayFailureRateLimitEnabled = true
	setting.RelayFailureRateLimitCount = 2
	setting.RelayFailureRateLimitDurationSeconds = 30
	t.Cleanup(func() {
		setting.RelayFailureRateLimitEnabled = previousEnabled
		setting.RelayFailureRateLimitCount = previousCount
		setting.RelayFailureRateLimitDurationSeconds = previousDuration
	})

	router := gin.New()
	router.GET(
		"/relay",
		func(c *gin.Context) { c.Set("token_id", 7) },
		RelayFailureRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusServiceUnavailable) },
	)

	for range 4 {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/relay", nil))
		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	}
}

func TestRelayFailureRateLimitBlocksAfterFailedRecoveryProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)
	previousEnabled := setting.RelayFailureRateLimitEnabled
	previousCount := setting.RelayFailureRateLimitCount
	previousDuration := setting.RelayFailureRateLimitDurationSeconds
	setting.RelayFailureRateLimitEnabled = true
	setting.RelayFailureRateLimitCount = 2
	setting.RelayFailureRateLimitDurationSeconds = 30
	t.Cleanup(func() {
		setting.RelayFailureRateLimitEnabled = previousEnabled
		setting.RelayFailureRateLimitCount = previousCount
		setting.RelayFailureRateLimitDurationSeconds = previousDuration
	})

	router := gin.New()
	router.GET(
		"/relay",
		func(c *gin.Context) { c.Set("token_id", 7) },
		RelayFailureRateLimit(),
		func(c *gin.Context) {
			c.Set("use_channel", []string{"9"})
			c.Status(http.StatusServiceUnavailable)
		},
	)

	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/relay", nil))
		return recorder
	}

	assert.Equal(t, http.StatusServiceUnavailable, request().Code)
	assert.Equal(t, http.StatusServiceUnavailable, request().Code)
	assert.Equal(t, http.StatusServiceUnavailable, request().Code, "one recovery probe is allowed")
	limited := request()
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	assert.Equal(t, "30", limited.Header().Get("Retry-After"))
}
