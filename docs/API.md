# 账号、题库与提交 API 说明

批次 6 的 SSE、SPJ 配置、输出型提交、重判与周期榜接口见 [扩展说明](EXTENSIONS.md)。

批次 5 的管理监控、CSV、封榜与导出接口见 [运维和管理说明](OPERATIONS.md)。超级管理员后台为 `/admin`；比赛操作仍按 contest_admins 作用域校验。

批次 4 的题单、讨论、题解、日常统计及三个全局榜接口见 [社区与全局榜](COMMUNITY.md)。公开读取允许匿名访问；写接口沿用会话、CSRF、Origin 校验及内容权限。

Markdown 安全预览使用 `POST /api/admin/problems/markdown-preview`（超级管理员）和 `POST /api/community/preview`（已登录账号）。外部题包使用 `POST /api/admin/problems/package-preview` 与 `package-import`，采用 multipart 的 `archive` ZIP 或内置样例 `sample=1`；错误或预览不会写入题目。

测试中心管理员接口为 `POST /api/admin/operations/simulate`、`POST /api/admin/operations/reset`、`POST /api/admin/operations/rejudge/problem` 与 `/rejudge/contest`。范围重判请求使用 `{key,reason,problem_id}` 或 `{key,reason,contest_id}`；初始化请求使用 `{confirmation,reason}`，确认词必须精确为 `初始化全部业务数据`。这些接口仍受登录、永久超级管理员、Origin、CSRF 和请求 ID 审计约束。

批次 3 的比赛、报名、队伍、身份审核和结算接口见 [比赛 API 与规则](CONTESTS.md#页面与接口)。比赛提交沿用 `/api/submissions`、`/api/runs`，增加可选 contest_id；不接受客户端指定队伍或用户身份。

本文件描述认证及相关公开 API；2D 已通过真实 MySQL/Redis、四语言 Docker 和故障恢复验收。具体检查范围与结果见 CHANGELOG.md，不把该结果扩展为生产容量或全部浏览器流程验收。

Run/Submit 创建、本人记录、语言列表、状态与错误码见 [提交说明](SUBMISSIONS.md)。提交 JSON 上限 256 KiB，源码与 Run 输入各 64 KiB；GET /api/languages 与 CSRF 获取一样不依赖数据库就绪。新任务保存为 Pending，由 2D Worker 与独立 Docker Runner 处理；内部认证协议见 [Runner 说明](RUNNER.md)。

2B 题目列表、题面、管理和 ZIP 导入接口见 [题目管理说明](PROBLEMS.md)。题目 JSON 写请求上限 256 KiB，ZIP 导入整个请求上限 34 MiB；认证仍为 16 KiB。题库另使用 403/40302 表示管理权限不足、413/41301 表示上传超限。所有管理写请求必须通过 CSRF header 校验，服务在鉴权前不解析 multipart 正文。

HTTP 层已迁至 Gin v1.12.0，HTTP 服务底层基于 Go net/http，API 路由、中间件和参数绑定统一使用 Gin。实施计划 1C 的本地回归已通过，本文的接口路径、请求字段、响应结构、错误码、Cookie、CSRF、来源校验与限流约定保持兼容；真实服务及容器验收仍待完成。

## 通用约定

- JSON 返回结构固定为 `{ "code": 0, "message": "success", "data": {} }`。错误的 data 为 null。
- 以下认证写接口使用 `Content-Type: application/json`，请求体上限 16 KiB，拒绝未知字段、多份 JSON 和错误类型。退出接口也允许完全不发送请求体。
- 所有接口响应使用 `Cache-Control: no-store`；账号 Cookie 和密码哈希不在 JSON 中返回。
- 写接口需要 CSRF Cookie 及 `X-CSRF-Token`；浏览器通过同源 Fetch 携带 Cookie。先请求 GET /api/auth/csrf 获取 token。
- 写请求的 Origin 如存在，必须匹配 PUBLIC_URL 的协议、主机和端口；HTTPS 下 gorilla/csrf 同时执行对应的来源检查。来源错误返回中文说明和安全错误数据中的 request_id，页面错误也附带同一请求 ID，便于在 Web 日志、Nginx 访问日志及审计日志之间定位。
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
- GET /ready：检查 Web 的 MySQL、Redis、迁移版本及 dirty 标记，全部满足返回 200，否则 503；Worker、dispatcher 和 Runner 使用各自的探针，不把 Web 就绪误作整个判题集群已就绪。
