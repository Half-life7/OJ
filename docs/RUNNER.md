# 2D Worker、独立 Runner 与 Docker 沙箱

批次 5 已交付生产内部 TLS、部署/备份恢复和 arm64 自定义镜像构建/仿真启动；当前部署流程和平台待验项见 [运维说明](OPERATIONS.md)。下文保留 2D 协议与历史验收边界。

2D 已包含 Outbox、常驻 Worker、租约/心跳、固定版本请求、四语言 Docker Runner、结果落库后 ACK、执行重试/死信、Redis 对账恢复、消息隔离与死信 CLI。当前构建迁移版本为 **8**（批次 5）。已在 Docker Desktop Linux/amd64、真实 MySQL/Redis 上完成四语言及故障恢复验收，详见 CHANGELOG.md；Linux/arm64 已交叉编译并仿真启动，不能当作原生整机验收。

## 运行与部署

开发 Compose 包含 MySQL、Redis、migrate、Web、dispatcher、worker、runner、Nginx 以及四个语言镜像准备服务。后四者只构建镜像并以 `/bin/true` 成功退出，Runner 在它们完成后启动；它们不执行提交代码。

1. 复制 deploy/.env.example 为 deploy/.env，填写数据库/Redis 密码、三个业务密钥及第四个独立 RUNNER_TOKEN。
2. Linux 上用 `stat -c '%g' /var/run/docker.sock` 查询 Docker Socket 组，写入 DOCKER_GID；需要 Docker Engine 27+（API 1.46+）、Linux 容器、seccomp 与内存/CPU/PID cgroup 支持。
3. 升级前备份 MySQL、problem-data 和 runner-data。新构建需先执行迁移 5；Compose 的 migrate 一次性服务会在 Web/Worker/dispatcher 启动前迁移。

```text
docker compose --env-file deploy/.env -f deploy/docker-compose.yml config --quiet
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up --build -d
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps -a
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs --tail 100 worker runner dispatcher migrate
```

四个语言镜像固定为 biue-lang-cpp17/go/python3/java:0.1.0，构建基线分别为 gcc:14.2.0-bookworm、golang:1.25.4-bookworm、python:3.12.10-slim-bookworm、eclipse-temurin:21.0.7_6-jdk-jammy，已完成本次实际下载/构建。Runner 检查语言标签、拒绝声明额外数据卷的镜像，并将镜像 ID 固定至进程生命周期；镜像重建后需重启 Runner。若构建器的认证 DNS 请求失败，可先用 `docker pull` 预拉取对应基础镜像，再构建；本次采用该方式，没有修改全局 DNS 或 Docker 凭据。

单独运行服务时按 README 注入业务环境，Worker 额外配置 RUNNER_URL、RUNNER_TOKEN；Runner 只需要自身环境及 Docker Socket，不接收业务数据库密码：

```text
go run ./cmd/dispatcher
go run ./cmd/worker
go run ./cmd/judge-runner
```

Runner 的真实执行仅支持 Linux；Windows 可进行普通测试和交叉编译。Worker 默认串行，可通过 Compose `--scale worker=2` 增加进程，每个进程自动生成唯一消费者名。Runner 默认最多两个并发任务，可用 JUDGE_CONCURRENCY 调整为 1～8。

## 配置与权限

| 配置 | 默认/作用 |
| --- | --- |
| RUNNER_URL | Worker 必填，HTTP(S) origin；Compose 默认 http://runner:8083 |
| RUNNER_TOKEN | Worker/Runner 共享的独立随机凭据，32～256 字节，不得复用 JWT/CSRF/应用加密密钥 |
| WORKER_CONSUMER | 可选；未提供则自动生成随机唯一名 |
| WORKER_HEALTH_ADDR | 单机 127.0.0.1:8081，Compose 0.0.0.0:8081 |
| DISPATCHER_HEALTH_ADDR | 单机 127.0.0.1:8082，Compose 0.0.0.0:8082 |
| RUNNER_ADDR | 单机 127.0.0.1:8083，Compose 0.0.0.0:8083 |
| RUNNER_DATA_DIR | 默认 oj-data/runner；Compose 独立 runner-data 卷 |
| DOCKER_SOCKET | 默认 /var/run/docker.sock，仅 Runner 使用 |
| JUDGE_CONCURRENCY | 2，范围 1～8 |
| RUNNER_TLS_CERT / RUNNER_TLS_KEY | 生产环境必填，成对配置 |
| RUNNER_CA_FILE | Worker 与 Runner 健康探针可选的额外受信 CA |

Web、Worker、dispatcher、migrate 均无 Docker Socket。Runner 使用独立 judge 网络，不接入 MySQL/Redis 的 backend 网络，不映射宿主机端口；仅 Worker 同时连接业务与 judge 网络。沙箱使用 `network=none`，不接入上述任何业务网络。Runner 仍是能管理 Docker 的高权限信任边界，应使用专用宿主机/引擎；本实现没有给普通业务服务提供 Docker 透传接口。

生产环境 Worker 强制 HTTPS、Runner 强制配置证书。提供 deploy/docker-compose.runner-tls.yml 覆盖配置；将 ca.pem、server.pem、server.key 放入 deploy/runner-tls（已从 Git 和构建上下文排除），确保证书 SAN 包含 runner 与 127.0.0.1，并让 UID 10001 可读服务密钥。用两个 `-f` 参数同时加载基础与覆盖文件。此覆盖只处理 Runner 内部 TLS，Web 的生产 HTTPS 入口仍属于后续部署批次。

## 调用协议与恢复

- `POST /v1/judge`：Bearer 认证；application/json 请求为 `{request: judge.Request, lease_token: string}`。只接受语言 ID、源码、输入、固定限制与任务身份；没有可选宿主机命令、镜像或挂载路径。
- 响应为 application/x-ndjson：可选 Compiling、执行时的 Running 和唯一最终 Result。每行一个 judge.Event。客户端校验阶段顺序、任务身份、全部评分/资源约束、未知字段、UTF-8、事件数量及大小；单行最多 512 KiB，总读取最多 2 MiB。
- 客户端禁止环境代理和 HTTP 重定向，连接/握手/响应头均有有限超时；总预算为 60 秒加每个点的题目时限与 5 秒控制开销。坏协议走基础设施失败，容量繁忙的 429/503、租约丢失、数据库写入未知和进程退出均保留消息，避免错误消耗重试预算。
- Worker 每次领取前检查容量就绪；租约固定 60 秒、每 20 秒续期。阶段按有效租约持久化。接管同 attempt 时跳过已经持久化的较早阶段事件，不回退数据库状态。
- Runner 在执行前同步写入本地 journal，记录任务身份的请求指纹，不记录源码/隐藏测试输入/租约。执行和清理后同步保存结果；相同任务身份只能重放相同请求。不同请求复用身份被拒绝，新的租约接管会取消旧执行并等待其清理，不启动同 attempt 的第二个沙箱。
- runner-data 用 Linux 文件锁保证一个 journal 只有一个 Runner 进程。重启先清理该 journal owner 的遗留容器，未完成记录返回可重试 SystemError；已完成记录重放结果。多 Worker 可共享一个 Runner；不要把多个无共享 journal 的 Runner 放在随机负载均衡器后，也不要克隆同一个 owner 的 journal 同时运行两套 Runner。
- 每个容器都带 owner/task/到期标签，结束后强制移除。移除失败使 Runner 暂停就绪并记录清理债务，后台补偿重试；周期扫描仅清理自身 owner 的过期容器，启动扫描清理此前所有自身容器。
- `/health` 表示进程存活；带认证的 `/engine-ready` 检查引擎、镜像和清理状态；`/ready` 额外检查空余执行名额。容器健康探针使用 engine-ready，避免把正常忙碌误报为服务故障。

## 沙箱边界与判题规则

一次编译使用独立容器；每个测试点使用全新容器。Python 跳过编译；Java 要求默认包中的 Main 类。编译后仅导出 main 或受控默认包 .class 文件，拒绝链接/危险路径，产物最多 16 MiB、256 项。缺失或超预算产物按 CompileError 处理。

| 资源 | 限制 |
| --- | --- |
| 用户与权限 | UID/GID 10001，非特权，cap-drop ALL，no-new-privileges，Docker 默认 seccomp 必须启用 |
| 网络与文件 | network none、只读 rootfs、无宿主机绑定；仅 /work、/tmp 为有界 tmpfs |
| CPU/PID | 1 CPU，128 PID |
| 内存 | 执行按题目 MiB 限制，禁额外 swap；编译固定 1 GiB |
| 时间 | 执行按题目墙钟时限；编译 30 秒；控制、取消及清理另有外部超时 |
| 临时空间 | 执行 /work 64 MiB、/tmp 32 MiB；编译 /work 128 MiB、/tmp 512 MiB；/dev/shm 16 MiB |
| 单文件/描述符 | fsize 32 MiB，nofile 执行 64、编译 1024；禁 core dump |
| 输出 | Run stdout 64 KiB；Submit stdout 16 MiB；stderr 64 KiB；超限取消进程，执行记 RuntimeError |

不可变监督程序通过 Docker attached stdin 接收有界文件包，写入自身活跃 tmpfs；结果与编译产物经签名 stdout 返回。没有依赖 Docker archive API 将文件拷入 tmpfs（该路径在真实运行中存在挂载可见性差异）。Docker 容器日志关闭，避免额外持久化编译产物和运行输出。

监督程序使用固定命令，捕获输出、墙钟时间及 Linux wait 的子进程峰值 RSS。它关闭 dumpable，阻止同 UID 代码读取监督程序的 environ/内存/fd；子进程环境不含签名密钥。结果用每容器随机 HMAC 密钥签名，Runner 验证后才接受，提交代码不能通过输出伪造控制记录。隐藏答案从不进入沙箱，只在 Runner 外部比较。

OOM 通过 Docker 状态/事件确认，事件接口使用数字 Unix 秒/纳秒时间戳并容忍通知的短暂延迟；普通 SIGKILL 不直接当作内存超限，Java OutOfMemoryError 也归为 MLE。没有可测子进程内存的 OOM 路径标记为未测量并保存零值，避免虚构资源统计。容器内存限制包含监督程序开销，报告值为用户子进程峰值 RSS。上述限制已通过本次 Linux/amd64 实际验收。

默认比较器按 ASCII 空白分隔 token 精确比较，不提供浮点容差或 Special Judge。Submit 运行全部测试点，逐点给分，汇总首个失败状态及最大时间/内存；程序输出、隐藏输入/答案不写入对外结果。Run 使用自己的输入，不与隐藏答案比较且永远零分。编译诊断会有界转为合法 UTF-8。

## Redis 恢复与人工处理

dispatcher 每 30 秒有界扫描已派发超过 60 秒的活动任务/死信意图。记录的 Stream 消息缺失或身份不匹配才重开意图；Redis 暂时不可用不会据此重开。允许重发与终态提交并发，Worker 通过 MySQL 结果/失败证明安全确认重复消息。

迁移 5 增加 judge_message_quarantine。格式错误、或无法解析到活动任务且无结果/失败证明的消息，先保存 Stream ID、SHA-256 摘要及受控原因，再 ACK；没有将原始消息写入数据库或日志。有效的当前任务不能被未解析路径隔离。

死信命令要求数据库中的活跃永久超级管理员身份。人工重试检查当前 generation、准入容量与终态，创建新 generation/attempt 1，保留旧结果、旧失败和死信，固定题目版本不变，并写审计。相同预期 generation 重放不会重复创建任务。

```text
go run ./cmd/ojctl judge dead-list --actor-user-id 1 --limit 20
go run ./cmd/ojctl judge retry --actor-user-id 1 --dead-letter-id 7 --generation 1
```

## 真实环境验收

普通 `go test ./...` 不启动容器。已提供显式启用的实际 Docker 验收：在 Linux 构建四语言镜像后，运行：

```bash
BIUE_DOCKER_TEST=1 go test ./internal/judge_runner -run '^TestDockerIntegration$' -count=1 -timeout=20m -v
```

覆盖四语言 AC、WA、CE、TLE、MLE、RE、输出超限、PID、文件大小、描述符限制及非 root/只读根目录/禁网/监督程序 secret 隔离，并补偿清理本次生成的容器。请只在专用开发/验收 Docker 引擎上执行。

完整 Compose 启动并初始化管理员后，设置 BIUE_URL、BIUE_SMOKE_USERNAME 和 BIUE_SMOKE_PASSWORD，运行 `python tools/verify_2d.py`。它通过真实 HTTP 登录、创建 A+B 题目、导入 ZIP、提交四语言及 Run、轮询并检查分数/输出隔离；保留题目和提交历史，不清库。密码不输出，脚本只使用 Python 标准库。

可选 `BIUE_SMOKE_VERDICTS=1` 额外检查 HTTP CE/WA/RE/TLE/MLE、Run 输出超限及最终数据库摘要；配置 JSON 可用 `docker compose ... config --format json | python tools/check_2d_compose.py` 校验角色、网络、Socket 与密钥分离，检查器不会输出凭据。

可选 `BIUE_SMOKE_RECOVERY=1` 启用完整故障验收，并要求 BIUE_SMOKE_PROJECT 为独立的 biue-2d-* 项目、BIUE_SMOKE_ENV_FILE 位于工作区 .cache。它会停止/重启该项目的 Worker/Runner、清空该项目 Redis，并临时移动新建测试题的数据目录；不对生产项目执行，也不删除数据卷或历史。BIUE_SMOKE_RECORD 可指定保存不含凭据的验收 ID 记录。

本次已验证空库/重复迁移、实际 CHECK/历史外键、固定版本/Run 输入、Worker/Runner SIGKILL 恢复、Redis 清空与补投、防重复结果、第四次失败死信及经审计的人工重判。生产容量、长期压力、灾备恢复及原生 arm64 运行验收属于后续部署批次，不以本次单机验收替代。

独立验收项目 `biue-2d-20261110004742` 完成后已关闭，数据卷保留。忽略文件 `.cache/2d-acceptance.env` 仅供本机复用验收凭据，不提交 Git。

协议与参数采用 [Docker Engine API](https://docs.docker.com/reference/api/engine/version/v1.46/) 的受控子集；服务没有 Docker API 透传端点。
