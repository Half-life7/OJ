# Outbox 与死信派发（2D 第一、四小批）

独立 `cmd/dispatcher` 将已提交的 Outbox 意图投递至 `oj:judge`，并将持久化死信投递至 `oj:judge:dead`。当前构建要求迁移 5（含重试、死信及消息隔离）；进程没有业务写 HTTP 接口，不需要 Docker Socket，已接入 Compose 和独立健康探针。

## 运行

按 README 为进程注入环境变量，备份后先运行 `go run ./cmd/ojctl migrate up` 至版本 5，再在独立终端运行：

```text
go run ./cmd/dispatcher
```

配置校验沿用应用配置，包括数据库、Redis、时区和密钥。每轮检查 MySQL、Redis、迁移版本及 dirty 状态，然后分别尝试派发一条普通任务和一条死信；依赖不可用时每秒重新检查，恢复后自动继续。空队列或派发错误后每秒轮询，收到中断或 SIGTERM 后取消当前操作并关闭连接。

## 一致性

- 每次使用 `FOR UPDATE SKIP LOCKED` 锁住一条未派发意图，可并行运行多个派发进程；普通任务还要求 available_at 不晚于数据库当前时间。每次普通/死信派发各有 5 秒上下文上限。
- 严格校验 task/submission/generation/attempt，普通 Stream 只携带这四项标识，不携带源码、测试数据或路径。死信另带 dead_letter_id 和受控 reason，且只接受 attempt=4。
- 先增加派发尝试次数，再 XADD；只有 XADD 成功才记录 stream_id 和 dispatched_at。发送失败时保留未派发状态，并尽可能提交尝试计数。
- 发送成功后数据库更新失败、提交失败或进程崩溃可能造成重复投递；消费端必须按任务身份与租约防重。未登记成功的意图会再次投递。
- 不使用 MAXLEN 截断队列，避免删除尚未处理的消息。
- 派发不修改任务或提交状态；Pending 由常驻 Worker 通过 CAS 推进，再调用独立 Runner 获取真实结果。

派发登记不能替代 Redis 持久化。当前实现每 30 秒有界扫描派发超过 60 秒的活动任务/死信记录，仅当 Stream 中记录的消息缺失或身份不匹配时重开意图；Redis 查询失败不推断数据丢失。正常和死信分别保存扫描游标，每轮最多各扫描 32 条。允许与终态提交并发，重复投递仍由数据库结果/失败证明安全确认。常驻消费者、Runner、人工重试及 Compose 说明见 [RUNNER.md](RUNNER.md)。

## 验证边界

sqlmock 与 miniredis 覆盖普通/死信发送、空队列、非法任务或原因码、Redis 故障、登记失败和提交失败，验证至少一次投递语义；SQL 期望校验普通派发的 available_at 条件。2D 已实际验证三次退避重试、第四次失败死信、Redis 清空补投、MySQL/Redis 重启、Worker/Runner SIGKILL、重复消息与隔离确认。生产级行锁并发压力及容量另行验收。
