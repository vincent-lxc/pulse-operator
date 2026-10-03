// Package contract 保存 operator 服务可被任意层引用的无依赖契约。
// 本包不得导入其他包，也不得包含数据库模型、运行时对象或请求级状态。
package contract

// ServiceName 是 operator 在配置、ServiceContext 和路由中的稳定服务名。
const ServiceName = "operator"
