# Worker 消费、租约、结果与重试（2D）

最新状态：2D 已完成，常驻 `cmd/worker`、独立 Docker Runner、Redis 对账与消息隔离、死信查看/人工重试 CLI 及 Compose 均已通过真实 MySQL/Redis/Linux amd64 验收。当前构建迁移版本为 9（批次 5 交付见 OPERATIONS.md）。运行与验收说明见 [RUNNER.md](RUNNER.md)；以下保留各模块的事务与协议约定。

## 第四小批实施约定（已完成，本地验证）

- 仅受控基础设施故障码触发执行重试，程序自身的 CE/WA/TLE/MLE/RE 不自动重试。attempt=1～3 失败后，同事务记录失败、增加 attempt、清空租约、将任务与提交恢复 Pending，并创建下一 attempt 的 Outbox；退避分别为 1/2/4 秒。
- 第四次基础设施故障同事务记录失败、保存 SystemError 终态和零分历史、清空租约、保存持久化死信及派发意图；死信流为 `oj:judge:dead`。
- MySQL 为事实来源，旧消息只有在查询到该 attempt 的持久化失败决策或终态结果后才能 ACK；数据库提交未知、Redis 故障或派发登记失败都允许安全重试。
- 新增迁移 4，记录每次执行失败与死信；正常/死信派发都采用至少一次语义。失败与死信只保存受控原因码，不保存源码、隐藏输入输出、令牌或底层异常文本。
- 第四小批的失败决策模块已由常驻 Worker 调用；Runner、消息隔离、对账补投及死信人工重试 CLI 已继续接入。死信管理网页留在后续完整后台批次。

## 第三小批实施约定（已完成，本地验证）

- 请求只读取 submission 固定的 statement_version/data_version；Run 使用创建时保存的 stdin，Submit 校验数据卷指纹、点数、总字节和总分与数据库一致，不依赖题目当前可见性或版本指针。
- 阶段推进与结果提交按 submission→task 顺序加锁，检查 generation/attempt/token/有效期及双方状态；终态只追加一次结果，汇总与逐点结果同事务保存。
- Runner 结果必须匹配固定请求，检查合法终态、测试点身份、分数、通过数、资源统计及输出上限；Submit 不保存程序输出或运行诊断，编译错误除外。
- 心跳续期失败时取消执行上下文；ACK 前必须查询到匹配任务身份和 attempt 的持久化终态结果。提交失败或提交结果未知时保留消息，恢复时通过已保存结果补 ACK。
- 第三小批自身未新增 HTTP/迁移/权限；后续接入状态以本文件顶部和 RUNNER.md 为准。

`internal/judge` 的 Queue、LeaseStore、ExecutionStore、Consumer 已由常驻 Worker 串联。第四小批新增迁移 4，第五次迁移增加消息隔离；独立 Runner 的认证接口、配置和 Socket 权限边界见 RUNNER.md。Web 和 Worker 没有 Docker Socket。

## 消费流程

1. `NewQueue(redis, consumerName, reclaimIdle)` 要求唯一的进程消费者名，仅允许 ASCII 字母、数字、连字符和下划线，最长 128 字节；重新领取阈值为 1 秒至 1 小时。
2. `Receive` 通过 `XGROUP CREATE oj:judge judgers 0 MKSTREAM` 幂等创建消费者组，从头读取派发早于 Worker 启动的消息。
3. 每次先执行 `XAUTOCLAIM` 扫描长期未确认消息，保存扫描游标；没有可领取消息时执行非阻塞 `XREADGROUP` 读取一条新消息。调用方在空队列或故障时应退避。
4. `Consumer.Next` 串联接收和 MySQL 租约领取。返回 `acquired` 时才携带可用租约；`busy` 表示仍有有效持有者，`obsolete` 表示记录缺失、轮次不符或任务已经终结。
5. 非法消息只保留消息 ID 和 invalid 标志，不返回原始字段；后续须先持久化隔离记录再 ACK。接收与领取阶段对所有消息都不 ACK。

Redis 消息拥有者与执行权限独立。消息可以被替代消费者重新领取，但它必须成功领取数据库租约后才能继续处理；Redis 的 idle 时间不会推翻有效数据库租约。

## 租约规则

- `NewLeaseStore(db, ttl)` 接受 10 秒至 5 分钟的租约；`Claim` 和 `Renew` 各有 5 秒上下文上限。
- Claim 事务依次锁定 submission 与 task，检查完整任务身份、当前 generation/attempt、双方状态一致性和终态。
- 使用 MySQL `UTC_TIMESTAMP(6)` 判断是否过期，避免 Worker 主机时钟偏差。首次领取将任务与提交从 Pending 同事务推进至 Queued。
- 领取生成 128 位随机令牌；有效租约返回 busy，过期租约允许更换令牌并保留当前执行阶段。事务提交失败不返回可用租约。
- Renew 必须匹配 task/submission/generation/attempt/token，且任务非终态、原租约未过期。失效返回 `ErrLeaseLost`；持有者必须停止后续操作。
- 租约令牌只供内部调用，不应出现在 HTTP、日志或对外诊断中。

重新领取是同一 attempt 的消息恢复，并不创建一次新的执行重试。后续执行器必须先核对 Runner 中已有执行的身份；不能因消息再次出现就无条件启动另一个沙箱。Runner 幂等调用、阶段更新与结果回写同样必须核对有效租约令牌。基础设施故障重试单独增加 attempt，最多初次加三次重试。

## 验证与后续

miniredis 验证组创建前消息不丢失、有效期内不抢取、过期消息转交新消费者、非法消息保留未确认状态、Redis 故障恢复。sqlmock 验证新领取、有效租约防重、过期接管、旧轮次拒绝、状态不一致拒绝、更新与提交失败回滚、续期完整 CAS 条件与失效令牌。组合测试覆盖同一条消息在替代消费者中获得新令牌并保持未 ACK。

第三小批增加固定版本请求加载、结果校验、阶段 CAS、心跳取消、汇总与逐点结果原子保存、提交后 ACK 与提交结果未知后的补 ACK 测试；真实数据卷用于验证固定数据的读取与摘要、点数、字节和分值核对。

第四小批增加 1～3 次失败重试、第四次失败终结与死信、失败处理后 ACK、提交未知后的恢复、旧 attempt 防重复、Runner SystemError 路由和死信派发失败测试；迁移测试验证嵌入文件链与 ExpectedVersion 一致。

已在真实环境验证迁移/约束、重试与死信、Redis 清空补投及服务重启、跨进程崩溃恢复和人工重判；独立 Runner、常驻 Worker、非法消息隔离及 Compose 的实际记录见 CHANGELOG.md。生产压力、身份并发唯一性和原生 arm64 整机验收不在本次单机结果内。

## 请求、执行与提交调用约定

- `NewExecutionStore(leases, datasetReader)` 使用和 Consumer 相同的 LeaseStore；生产数据读取应使用 `problemdata.Store`，其 Read 会检查文件集合、大小、逐文件哈希与完整数据指纹。
- `LoadRequest` 的 SQL 按 submission 固定版本关联题面/数据，匹配当前有效租约。Run 输入使用已保存 stdin；即使题目样例修改、题目下架或版本升级，也不会改用当前题目数据。
- `Advance` 只接受 Compiling 或 Running，事务检查合法前向状态；相同阶段重放可成功，倒退和跳过必要前置阶段会被拒绝。无编译步骤的语言可 Queued→Running。
- `LeaseStore.WithHeartbeat` 先验证并续期，然后每 TTL/3 续期，失去租约或续期错误会取消工作上下文。执行器必须响应取消。将其用于请求加载、Runner 调用与阶段事件；执行结束、心跳退出后，再提交结果，避免将主动清理租约误判为心跳失效。
- 租约接管保留阶段；后续 Runner 必须按任务身份恢复既有执行，阶段重放不得把 Running 退回 Compiling。
- `Consumer.Complete` 校验领取身份，普通判题结果调用 `Finish` 成功后才调用 `AcknowledgeCompleted`；Runner SystemError 先按可信请求校验，再进入基础设施失败流程。Finish 会重新加载可信请求，不接受调用方自行改写的测试点或分值，也拒绝绕过重试流程直接保存 SystemError。
- `Finish` 同事务修改 task 终态并清理租约、插入 submission_results 历史与 submission_case_results、更新 submission 汇总；最终 CAS 还会检查租约有效期，防止校验期间过期的持有者写回。终态不可覆盖，事务任何一步失败均不 ACK。
- 恢复遇到已完成任务时调用 `AcknowledgeCompleted`：查询完整任务身份及 attempt 的持久化终态，再核对 Stream 消息 ID 实际载荷，最后 XACK。已重试或已死信的失败 attempt 可调用 `AcknowledgeFailed`，查询同一身份的持久化失败决策后再 ACK。Redis 失败或提交结果未知时可重试；没有匹配证明时拒绝 ACK。busy、过时轮次且无匹配历史、非法消息都保持待处理。

## Runner 结果契约

- TaskEnvelope 必须完全一致。输出与诊断各最多 64 KiB，并为合法 UTF-8；时间与内存非负，未测量内存时必须为零。
- Run 固定一个执行、零分、无逐点结果；Accepted 表示程序正常执行，不与隐藏答案比较，因此不接受 WrongAnswer。正常通过数为 1，失败为 0。
- Submit 正常执行要求返回固定请求中全部测试点，顺序和 ID 一致；通过点得该点完整分值，失败点为零。汇总总分与通过数必须等于逐点合计，时间/内存为逐点最大值，所有点的内存测量标志须一致。
- Submit 汇总状态为按请求顺序遇到的首个失败状态，全部通过为 Accepted。支持 WrongAnswer、TimeLimitExceeded、MemoryLimitExceeded、RuntimeError。
- CompileError、SystemError、Cancelled 不携带逐点结果或部分分数，通过数为零；仍报告固定任务的 total_cases。结果还必须满足数据库当前阶段的合法状态流转。
- SystemError 在 Consumer.Complete 中映射为 `runner_system_error` 基础设施故障，消费重试预算；直接 Finish 不接受该状态。第四次基础设施失败通过专用事务保存最终 SystemError。
- Submit 在写历史和汇总前统一清空程序输出，仅 CompileError 保留编译诊断；逐点表不保存测试输入、答案和程序输出。数据库现有版本没有独立保存 MemoryMeasured 标志，本批保留现有 schema，未测量值仅写零。

## 执行重试与死信

升级前备份数据库、problem-data 和 runner-data，执行 `go run ./cmd/ojctl migrate up` 至当前版本 9 后再启动新构建。真实数据库迁移是否已运行以 CHANGELOG.md 为准；普通测试仅验证迁移文件链和应用事务。

`ExecutionStore.Fail(ctx, lease, reason)` 不读取数据卷，也不直接访问 Redis。调用方须在有效租约下停止当前执行，随后记录受控基础设施故障：

| 原因码 | 含义 |
| --- | --- |
| runner_unavailable | Runner 连接或服务不可用 |
| runner_protocol | Runner 响应违反约定 |
| runner_system_error | Runner 返回合法 SystemError |
| data_unavailable | 固定数据无法读取或完整性校验失败 |
| execution_interrupted | 基础设施造成执行中断；主动取消任务应保存 Cancelled |

原因码不接受自由文本；底层异常、源码、输入输出和租约令牌都不写入失败日志或死信消息。CE/WA/TLE/MLE/RE 使用正常结果提交，不消耗基础设施重试预算。

| 失败 attempt | 原子处理 | 最早再次派发时间 |
| --- | --- | --- |
| 1 | 记录 retry，清理租约，恢复 Pending，创建 attempt 2 Outbox | 提交事务内数据库时间 + 1 秒 |
| 2 | 同上，创建 attempt 3 Outbox | + 2 秒 |
| 3 | 同上，创建 attempt 4 Outbox | + 4 秒 |
| 4 | 记录 dead，保存 SystemError 零分终态及结果历史，创建持久化死信 | 不再执行自动重试 |

重试保留 task ID、submission ID、generation 和固定题目版本，仅增加 attempt；清除汇总分数、通过数、执行资源及输出。它是专用失败事务内的受控状态回退，不修改普通 CanTransition 规则。旧 attempt/令牌不能重试第二次、续期或写入迟到结果。重试保持原活动任务，不额外占用准入名额。

`Consumer.Fail` 先提交上述事务，再以 `AcknowledgeFailed` 确认旧消息。新 attempt 的派发不必已完成，旧消息也可以 ACK，因为新意图已经在 MySQL 持久化。若提交响应丢失，不重复增加 attempt；恢复时先查询旧 attempt 的失败决策补 ACK。

第四次失败在同一事务写入 `judge_attempt_failures`、`submission_results`、task/submission 终态及 `judge_dead_letters`，没有逐点结果、程序输出或运行诊断。死信表也是 Outbox，已有 dispatcher 会独立调用 `DispatchDeadOne` 投递至 `oj:judge:dead`，只在 XADD 成功后登记。每轮分别处理正常任务和死信，避免正常队列持续有消息时饿死死信派发。

死信载荷为四项 TaskEnvelope 标识，加 `dead_letter_id`（持久化死信主键）及 `reason`。消息至少一次投递，管理工具按 dead_letter_id 去重；Redis 发布失败不改变 MySQL 终态，发布成功但登记失败允许重复投递。现已提供 `ojctl judge dead-list/retry`，要求活跃超级管理员并审计；未提供自动历史清理或管理网页/API。
