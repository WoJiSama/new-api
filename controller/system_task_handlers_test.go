package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelTestScheduleDecisionUsesAbsoluteDeadlines(t *testing.T) {
	now := time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC).Unix()
	interval := 20 * time.Minute

	_, due := channelTestScheduleDecision(now, now-10*60, 0, interval)
	require.False(t, due)

	payload, due := channelTestScheduleDecision(now, now-10*60, now-1, interval)
	require.True(t, due)
	require.Equal(t, channelTestTaskPayload{RecoveryOnly: true}, payload)

	payload, due = channelTestScheduleDecision(now, now-20*60, 0, interval)
	require.True(t, due)
	require.Nil(t, payload)
}
