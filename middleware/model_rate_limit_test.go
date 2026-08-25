package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestRelayFailureRateLimitBlocksOnlyRepeatedUnavailableResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)
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

	request := func(tokenID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/relay", nil)
		router.ServeHTTP(recorder, request)
		return recorder
	}

	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
	limited := request("7")
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	assert.Equal(t, "30", limited.Header().Get("Retry-After"))

	redisServer.FastForward(30 * time.Second)
	assert.Equal(t, http.StatusServiceUnavailable, request("7").Code)
}
