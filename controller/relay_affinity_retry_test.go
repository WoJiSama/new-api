package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTransientUpstreamFailure(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{name: "bad gateway", err: types.NewErrorWithStatusCode(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), want: true},
		{name: "too many requests", err: types.NewErrorWithStatusCode(errors.New("daily limit exceeded"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), want: true},
		{name: "service unavailable", err: types.NewErrorWithStatusCode(errors.New("service unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable), want: true},
		{name: "gateway timeout", err: types.NewErrorWithStatusCode(errors.New("gateway timeout"), types.ErrorCodeBadResponseStatusCode, http.StatusGatewayTimeout), want: true},
		{name: "deadline exceeded", err: types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError), want: true},
		{name: "bad request", err: types.NewErrorWithStatusCode(errors.New("invalid request"), types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTransientUpstreamFailure(tt.err), fmt.Sprintf("error=%v", tt.err))
		})
	}
}

func TestShouldRetryClearsStickyChannelAfterUpstream429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)
	originalRules := append([]operation_setting.ChannelAffinityRule(nil), setting.Rules...)
	modelName := fmt.Sprintf("gpt-affinity-429-%d", time.Now().UnixNano())
	setting.Rules = append([]operation_setting.ChannelAffinityRule{{
		Name:       "upstream-429-retry-test",
		ModelRegex: []string{"^" + modelName + "$"},
		PathRegex:  []string{"/v1/responses"},
		KeySources: []operation_setting.ChannelAffinityKeySource{{
			Type: "request_header",
			Key:  "X-Test-Affinity",
		}},
		TTLSeconds:         60,
		SkipRetryOnFailure: true,
		IncludeUsingGroup:  true,
		IncludeRuleName:    true,
	}}, originalRules...)
	t.Cleanup(func() { setting.Rules = originalRules })

	newContext := func() *gin.Context {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		ctx.Request.Header.Set("X-Test-Affinity", "session-429")
		return ctx
	}

	ctx := newContext()
	_, found := service.GetPreferredChannelByAffinity(ctx, modelName, "default")
	require.False(t, found)
	service.RecordChannelAffinity(ctx, 9)

	probe := newContext()
	channelID, found := service.GetPreferredChannelByAffinity(probe, modelName, "default")
	require.True(t, found)
	require.Equal(t, 9, channelID)

	err429 := types.NewErrorWithStatusCode(errors.New("daily limit exceeded"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	require.True(t, shouldRetry(probe, err429, 2))
	require.False(t, service.ShouldSkipRetryAfterChannelAffinityFailure(probe))

	afterClear := newContext()
	_, found = service.GetPreferredChannelByAffinity(afterClear, modelName, "default")
	require.False(t, found)
}
