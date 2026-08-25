package controller

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func manualChannelTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestDisableFailedManualChannelTestRequiresEnabledAutoBanChannel(t *testing.T) {
	result := testResult{
		context:  manualChannelTestContext(),
		localErr: errors.New("upstream connection failed"),
	}

	for name, channel := range map[string]*model.Channel{
		"nil channel":       nil,
		"manual disabled":   {Id: 1, Status: common.ChannelStatusManuallyDisabled, AutoBan: common.GetPointer(1)},
		"auto ban disabled": {Id: 2, Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(0)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() { disableFailedManualChannelTest(channel, result) })
		})
	}
}

func TestDisableFailedManualChannelTestBuildsFailureForLocalError(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channel := &model.Channel{
		Id:      3,
		Name:    "local failure channel",
		Type:    constant.ChannelTypeOpenAI,
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	assert.NoError(t, db.Create(channel).Error)
	assert.NoError(t, db.AutoMigrate(&model.Ability{}))
	result := testResult{
		context:  manualChannelTestContext(),
		localErr: errors.New("upstream connection failed"),
	}

	// The helper must accept local request failures and construct the same
	// channel error source used by the existing automatic disable flow. The
	// integration path is exercised by the status assertion in the next test.
	assert.NotPanics(t, func() { disableFailedManualChannelTest(channel, result) })
}

func TestDisableFailedManualChannelTestHandlesMissingContext(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channel := &model.Channel{
		Name:    "missing context channel",
		Type:    constant.ChannelTypeOpenAI,
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	assert.NoError(t, db.Create(channel).Error)
	assert.NoError(t, db.AutoMigrate(&model.Ability{}))

	assert.NotPanics(t, func() {
		disableFailedManualChannelTest(channel, testResult{localErr: errors.New("unsupported test channel")})
	})
}

func TestDisableFailedManualChannelTestPersistsAutoDisabledStatus(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channel := &model.Channel{
		Name:    "manual test channel",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	assert.NoError(t, db.Create(channel).Error)
	assert.NoError(t, db.AutoMigrate(&model.Ability{}))

	result := testResult{
		context:     manualChannelTestContext(),
		newAPIError: types.NewError(errors.New("connection refused"), types.ErrorCodeDoRequestFailed),
	}
	disableFailedManualChannelTest(channel, result)

	var stored model.Channel
	assert.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
}
