package biz

import (
	"errors"

	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/testutil"
)

var errDB = errors.New("db error")

// mustNewBiz 用可控的就绪桩与 mock model 构造领域服务，供测试隔离外部依赖。
func mustNewBiz(ready bool, mdl model.AuditLogModel) *AuditLogBiz {
	return NewAuditLogBiz(&testutil.ReadyStub{Ready: ready}, mdl)
}

func validLog() CreateLog {
	return CreateLog{ServiceName: "auth"}
}
