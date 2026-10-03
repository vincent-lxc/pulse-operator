// 本文件在没有 HTTP 请求的循环里生成主键。
package models

import (
	"sync"
	"time"

	"github.com/digitalwayhk/core/pkg/utils"
	"github.com/yitter/idgenerator-go/idgen"
)

var (
	idOnce sync.Once
	idMu   sync.Mutex
	worker idgen.ISnowWorker
)

func nextID() uint {
	idOnce.Do(func() {
		worker = utils.NewAlgorithmSnowFlake(1, 1)
	})
	idMu.Lock()
	defer idMu.Unlock()
	return uint(worker.NextId())
}

func touchNew(setID func(uint), setCreated func(time.Time), setUpdated func(time.Time), setHash func(string), hash string, id uint) uint {
	if id == 0 {
		id = nextID()
	}
	now := time.Now().UTC().Truncate(time.Second)
	setID(id)
	setCreated(now)
	setUpdated(now)
	setHash(hash)
	return id
}
