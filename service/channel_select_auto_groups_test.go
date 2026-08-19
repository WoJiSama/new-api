package service

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelSelectAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelDailyQuota{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))

	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))

		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	return db
}

func createChannelSelectAutoGroupsChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	createChannelSelectAutoGroupsChannelWithPriority(t, db, id, group, modelName, 0, "")
}

func createChannelSelectAutoGroupsChannelWithPriority(t *testing.T, db *gorm.DB, id int, group, modelName string, priorityValue int64, settings string) {
	t.Helper()
	priority := priorityValue
	weight := uint(100)
	require.NoError(t, db.Create(&model.Channel{
		Id:            id,
		Type:          constant.ChannelTypeOpenAI,
		Key:           fmt.Sprintf("key-%d", id),
		Status:        common.ChannelStatusEnabled,
		Name:          fmt.Sprintf("channel-%d", id),
		Weight:        &weight,
		Models:        modelName,
		Group:         group,
		Priority:      &priority,
		OtherSettings: settings,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestCacheGetRandomSatisfiedChannelSkipsDailyExhaustedPriority(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "daily-quota-priority-model"
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2201, "default", modelName, 10, `{"daily_quota_limit":10}`)
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2202, "default", modelName, 0, "")
	require.NoError(t, db.Create(&model.ChannelDailyQuota{
		ChannelId: 2201,
		Day:       time.Now().Format("2006-01-02"),
		Used:      10,
	}).Error)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	param := &RetryParam{
		Ctx:         ctx,
		ModelName:   modelName,
		TokenGroup:  "default",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(0),
	}

	selected, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 2202, selected.Id)
}

func TestCacheGetRandomSatisfiedChannelUsesSamePriorityRetryCandidatesBeforeFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "same-priority-rpm-model"
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2301, "default", modelName, 10, `{"same_priority_retry_rpm_limit":20}`)
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2302, "default", modelName, 10, `{"same_priority_retry_rpm_limit":20}`)
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2303, "default", modelName, 0, "")
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	retry := 1
	priority := int64(10)
	param := &RetryParam{
		Ctx:         ctx,
		ModelName:   modelName,
		TokenGroup:  "default",
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
	param.RecordSelectedChannel(&model.Channel{Id: 2301, Priority: &priority})

	samePriority, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, samePriority)
	assert.Equal(t, 2302, samePriority.Id)

	param.ExcludeChannel(samePriority)
	fallback, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, fallback)
	assert.Equal(t, 2303, fallback.Id)
}

func TestCacheGetRandomSatisfiedChannelSkipsUnconfiguredSamePriorityRetry(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "same-priority-rpm-disabled-model"
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2401, "default", modelName, 10, "")
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2402, "default", modelName, 10, "")
	createChannelSelectAutoGroupsChannelWithPriority(t, db, 2403, "default", modelName, 0, "")
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	retry := 1
	priority := int64(10)
	param := &RetryParam{
		Ctx:         ctx,
		ModelName:   modelName,
		TokenGroup:  "default",
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
	param.RecordSelectedChannel(&model.Channel{Id: 2401, Priority: &priority})

	selected, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 2403, selected.Id)
}

func TestCacheGetRandomSatisfiedChannelUsesTokenAutoGroupsWhenGlobalAutoIsEmpty(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-groups-runtime-model"
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}

	first, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 2101, first.Id)
	assert.Equal(t, "vip", selectedGroup)
	assert.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
	assert.Empty(t, setting.GetAutoGroups(), "the selection must not depend on the global Auto list")

	param.IncreaseRetry()
	second, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 2102, second.Id)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
}
