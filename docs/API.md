# 首批 API 说明

本文件描述 1B 已编写的接口。真实 MySQL/Redis 和容器验收尚未运行；具体检查结果见 CHANGELOG.md。

## 通用约定

- JSON 返回结构固定为 `{ "code": 0, "message": "success", "data": {} }`。错误的 data 为 null。
- 有请求体的写请求使用 `Content-Type: application/json`，请求体上限 16 KiB，拒绝未知字段、多份 JSON 和错误类型。退出接口也允许完全不发送请求体。
- 所有接口响应使用 `Cache-Control: no-store`；账号 Cookie 和密码哈希不在 JSON 中返回。
- 写接口需要 CSRF Cookie 及 `X-CSRF-Token`；浏览器通过同源 Fetch 携带 Cookie。先请求 GET /api/auth/csrf 获取 token。
- 写请求的 Origin 如存在，必须匹配 PUBLIC_URL；HTTPS 下 gorilla/csrf 同时执行对应的来源检查。
- 账号 ID 去除首尾空白并转为小写，3～32 个 ASCII 字母、数字、下划线，创建后不可修改。
- 密码保留原始字节，必须是有效 UTF-8、8～72 字节，不执行截断。确认密码必须与密码完全相同。
- MySQL/Redis 或迁移状态未就绪时，业务 API 返回 503。CSRF 获取及进程健康检查不依赖数据库就绪。

## 接口

| 方法与路径 | 请求 | 登录及权限 | 成功响应 |
| --- | --- | --- | --- |
| GET /api/auth/csrf | 无 | 匿名可用 | 200，data.csrf_token；设置 CSRF Cookie |
| POST /api/auth/register | username、password、password_confirmation、可选 student_no | 匿名可用，仅创建 permanent/user | 201，data 为脱敏用户；不自动登录 |
| POST /api/auth/login | username、password | 匿名可用 | 200，data 为脱敏用户，设置 biue_session Cookie |
| POST /api/auth/logout | 无请求体，或 JSON 空对象 {} | 无会话或失效会话也可幂等退出；有效会话必须完成持久化撤销 | 200，data 为 {}，清认证 Cookie |
| POST /api/auth/password | current_password、password、password_confirmation | 当前有效会话 | 200，data 为 {}，使所有旧会话失效并清 Cookie，需重新登录 |
| GET /api/users/me | 无 | 当前有效会话，只能查看本人 | 200，data 为脱敏用户 |

脱敏用户 data 示例：

```json
{
  "id": 7,
  "username": "alice",
  "account_type": "permanent",
  "global_role": "user",
  "status": "active",
  "display_name": "alice",
  "student_no": null,
  "student_no_status": "none"
}
```

学号未填写时为 null/none；注册填写后是 pending，不代表已验证。重复账号或学号只返回冲突，不透露占用者。身份审核、纠错和账号关联不在本批开放，客户端不能指定角色、账号类型、学号验证状态或 person_id。

## 会话与认证

- JWT 使用 HS256；验证签名算法、issuer、audience、exp、iat、nbf、jti、用户 ID 和 token_version。默认有效期 12 小时，无 refresh token。
- biue_session Cookie 为 HttpOnly、SameSite=Lax、Path=/；PUBLIC_URL 为 HTTPS 时启用 Secure。前端不使用 localStorage 保存 token。
- 每次鉴权从 MySQL 检查用户 active 状态、当前 token_version、对应持久化会话未撤销且未过期。Redis 清空不能让已退出的 JWT 重新生效。
- 登录在锁定用户后复查状态及版本，防止并发改密后签发旧版本会话；修改密码在用户锁下再次检查会话，再更新密码、版本和撤销记录。
- 退出、改密、注册、登录和管理员初始化成功操作记录审计；密码、JWT 和完整学号不写入通用操作日志。
- 超级管理员由 ojctl admin-init 创建，公共 API 无提权接口。全局角色是 user/super_admin；未来比赛管理员使用逐场授权，不凭全局角色扩大权限。

## 限流

Redis Lua 脚本原子更新来源 IP 和规范化账号的计数，键使用 SHA-256 摘要。仅信任配置中的代理网段提供的来源地址。

| 操作 | 时间窗口 | IP 上限 | 账号上限 |
| --- | --- | --- | --- |
| 注册 | 1 小时 | 10 次 | 5 次 |
| 登录 | 15 分钟 | 30 次 | 10 次 |
| 改密 | 15 分钟 | 30 次 | 当前会话 10 次 |

计数包含成功和失败的尝试。Redis 故障返回 503，不静默放行。限流状态丢失不会恢复已经撤销的认证会话。

## 错误码

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | 40001 | 请求格式、参数、密码边界或确认密码不合法 |
| 401 | 40101 | 账号、密码、状态或会话无效；不区分未知账号、错密码和禁用账号 |
| 403 | 40301 | CSRF 或来源校验失败 |
| 404 | 40401 | 路由不存在 |
| 405 | 40501 | 请求方法不允许 |
| 409 | 40901 | 账号或身份信息不可用 |
| 429 | 42901 | 请求次数超过限额 |
| 500 | 50001 | 未预期的内部异常 |
| 503 | 50301 | 依赖未就绪或存储操作失败 |

## 健康检查

- GET /health：进程可响应时返回 200，不承诺依赖或判题可用。
- GET /ready：检查 MySQL、Redis、迁移版本及 dirty 标记，全部满足返回 200，否则 503；不报告尚未实现的 Worker/Runner 为健康。
