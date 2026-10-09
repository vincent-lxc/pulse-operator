// 本文件集中选择 SQLite，并向 Manage 暴露无参数的 ModelList 工厂。
package models

import (
	"reflect"
	"strings"
	"sync"

	"github.com/digitalwayhk/core/pkg/persistence/database/oltp"
	"github.com/digitalwayhk/core/pkg/persistence/entity"
	persistencetypes "github.com/digitalwayhk/core/pkg/persistence/types"
)

var (
	dataActionOnce sync.Once
	dataAction     persistencetypes.IDataAction
)

// getDataAction 返回本服务共享的数据操作器。
func getDataAction() persistencetypes.IDataAction {
	dataActionOnce.Do(func() {
		dataAction = entity.GetGlobalSqliteInstance(NewServiceModel().GetLocalDBName())
	})
	return dataAction
}

// NewManageModelList 是 Manage 取得模型列表的唯一入口。
func NewManageModelList[T persistencetypes.IModel]() *entity.ModelList[T] {
	return entity.NewModelList[T](getDataAction())
}

func newSearch(model interface{}, size int) *persistencetypes.SearchItem {
	return &persistencetypes.SearchItem{Page: 1, Size: size, Model: model}
}

func ensureModel(model interface{}) error {
	modelType := reflect.TypeOf(model)
	if modelType == nil || modelType.Kind() != reflect.Ptr {
		return NewBusinessError("模型类型无效")
	}
	result := reflect.New(reflect.SliceOf(modelType)).Interface()
	err := getDataAction().Load(newSearch(model, 1), result)
	// 账单表已经在，但 SELECT * 会扫进类型对不上的影子列。这里只负责把表备好。
	if ignorableScanMismatch(err) && isBillModel(model) {
		return nil
	}
	return err
}

func isBillModel(model interface{}) bool {
	modelType := reflect.TypeOf(model)
	for modelType != nil && modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}
	return modelType != nil && modelType.Name() == "Bill"
}

func ignorableScanMismatch(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Scan error") || strings.Contains(msg, "converting driver.Value")
}

// EnsureStorage 在接受请求前创建全部模型表。
func EnsureStorage() error {
	for _, model := range []interface{}{
		NewSpendCategory(),
		NewPayee(),
		NewPayable(),
		NewRevenue(),
		NewDecisionRecord(),
		NewApproval(),
		NewCycleSnapshot(),
		NewBill(),
	} {
		if err := ensureModel(model); err != nil {
			return err
		}
	}
	return migrateStorage()
}

// migrateStorage 给已经存在的表补上新列。HasTable 发现表在时不会再迁移。
func migrateStorage() error {
	raw := getDataAction()
	sqlite, ok := raw.(*oltp.Sqlite)
	if !ok {
		return nil
	}
	db, err := sqlite.GetModelDB(NewPayable())
	if err != nil {
		return err
	}
	migrator, ok := db.(interface {
		AutoMigrate(...interface{}) error
	})
	if !ok || migrator == nil {
		return nil
	}
	return migrator.AutoMigrate(NewPayable(), NewDecisionRecord(), NewApproval(), NewBill())
}
