package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestIsTransientUpstreamFailure(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{name: "bad gateway", err: types.NewErrorWithStatusCode(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), want: true},
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
