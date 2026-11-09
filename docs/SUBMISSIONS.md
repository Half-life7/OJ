# 提交与任务基础（2C）

2C 已实现提交记录、版本固定、事务 Outbox、提交页面与本地编辑器，并通过本地验证。随后 2D 接入 Redis 派发、Worker 和独立 Docker Runner；当前新记录保持 Pending，不生成模拟判题结果。真实 MySQL 迁移、浏览器及容器验收仍待完成。

## 创建与读取

- 日常场景仅永久账号可用，contest_id 与 contest_team_id 均为 NULL；比赛提交将在比赛模块接入。
- POST /api/submissions 创建正式 Submit，字段为 problem_id、language、source_code。POST /api/runs 创建 Run，额外允许 stdin；省略 stdin 使用样例输入，空字符串表示显式空输入。Run 不能选择隐藏测试点，也不计入成绩。
- 题目必须为已发布的公开题目。Submit 还要求存在已导入的测试数据；创建时锁定题面和数据版本，后续编辑或重传不改变已有任务。
- source_code 为非空有效 UTF-8，最多 64 KiB；Run 输入最多 64 KiB；JSON 请求体上限 256 KiB。语言 ID 为 cpp17/go/python3/java，由固定语言注册表提供。
- GET /api/submissions 列出本人记录，支持分页、problem_id 和 kind=Run/Submit；GET /api/submissions/:id 只允许本人或超级管理员读取，其他用户获得 404。列表不返回源码，详情可返回本人的源码与 Run 输入/输出，正式测试的输入、答案和程序输出不对外公开。
- GET /api/languages 提供语言 ID 和显示名。页面提供本地 CodeMirror 6 编辑器、Run/Submit、个人提交记录与状态轮询；保持无前端运行时构建步骤。

## 可靠性与限流

- 创建事务在全局准入行锁内检查待处理任务数量，队列容量 JUDGE_QUEUE_CAPACITY 默认 1000，范围 1～100000；满额返回 503。提交、judge_task 和 outbox 同事务提交，Redis 暂时故障不会留下只有消息而没有事实记录的任务。
- Run 与 Submit 分开限流，Redis 原子更新账号/IP 计数：Submit 每分钟每账号 10 次、每 IP 30 次；Run 分别为 5 次和 15 次。超限 429，Redis 故障 503，不静默放行。
- 状态至少包含 Pending、Queued、Compiling、Running、Accepted、WrongAnswer、TimeLimitExceeded、MemoryLimitExceeded、RuntimeError、CompileError、SystemError、Cancelled；普通更新不得修改终态，Worker 通过 CAS 与合法流转推进状态。
- generation 从 1 开始标识一轮判题，attempt 从 1 开始标识该轮执行尝试。自动重试最多 3 次，即初次执行加 3 次重试；后续结果回写须匹配 task、generation、attempt 和租约令牌，迟到结果不得覆盖当前轮次。
- 固定 Redis Stream 为 oj:judge，消费者组为 judgers。2D 在 XADD 成功后标记 Outbox 已派发，允许重复派发；结果先持久化再 XACK。未确认消息可恢复领取，超出重试次数进入死信记录与流。

提交和结果永久保留，外键禁止级联删除；不将队列持久化测试当作真实沙箱执行验收。后续比赛计分与日常解题榜只消费有效 Submit 终态，Run 永不进入这些统计。

## 响应与部署

创建成功返回 201，data 包含提交 ID、kind、language、status、generation、固定的 statement_version/data_version、资源与得分统计及时间。列表 data 为 `{items,page,limit,total}`，默认每页 20 项，上限 100；仅详情返回源码。Run 的 stdin 与 output 仅允许有权读取该记录的用户查看，Submit 的 stdin/output 始终隐藏，错误诊断仅允许 CompileError。

| HTTP / code | 含义 |
| --- | --- |
| 400 / 40001 | JSON、语言、源码、输入或分页参数不合法 |
| 401 / 40101 | 未登录或会话失效 |
| 403 / 40302 | 账号不允许创建日常提交 |
| 404 / 40401 | 题目、提交不存在或无权读取 |
| 409 / 40902 | 正式提交的题目尚无可用测试数据 |
| 429 / 42901 | Run 或 Submit 独立限流超限 |
| 503 / 50302 | 待处理任务达到队列上限 |
| 503 / 50301 | 数据库、Redis 或其他业务依赖不可用 |

升级前备份数据库与题目数据卷，运行 `ojctl migrate up` 至版本 3，再启动 Web。CodeMirror 6 四语言资源已打包到本地静态文件，重建方法与第三方许可证见 tools/editor；Go/Docker 正常构建无需安装 Node。编辑器动态样式使用每请求 nonce，缺少 nonce 或初始化失败时保留 textarea。页面轮询采用退避，终态及离开页面后停止。
