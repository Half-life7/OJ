# 修订日志

每批记录目标、实际变更、数据库/API/权限影响、验证命令与结果、遗留项。状态只使用：进行中、已完成、待验证。

## 2026-11-09 — 计划落地

- 已阅读需求基线 plan.md，建立 docs/IMPLEMENTATION_PLAN.md。
- 已确认首批为工程基础与认证，学号归属 persons，真实运行环境验收暂缓。
- 当前环境：Windows/amd64，Go 1.25.4；Docker CLI 存在但引擎不可连接。
- 当前只有需求文档，无既有代码或 Git 仓库。本次不自动创建远程仓库或发布服务。

## 2026-11-09 — 1A 工程基础（待验证，代码已落盘）

- 建立 Go 工程、环境配置与脱敏 slog、Web 和 ojctl 入口、请求 ID、超时、异常恢复与优雅退出。
- 建立 MySQL/sqlx 和 Redis 连接池、独立迁移连接及嵌入 SQL。业务连接禁止 multiStatements；迁移不自动清除 dirty 状态。
- 新增 GET/HEAD /health、/ready；就绪检查包含 MySQL、Redis、迁移版本及 dirty。业务依赖故障时返回统一 503。
- 提供非 root 多阶段 Dockerfile、开发 Compose、Nginx、环境示例、忽略规则、README 和数据库/API 文档。MySQL/Redis 不映射宿主端口，未挂载 Docker Socket。
- 修正 IPv6 数据库地址拼接、Redis 默认重试参数、审计请求 ID 上下文、API 404/405 的 JSON 响应及日志查询参数脱敏。
- 数据库变更只写成迁移文件，没有连接或变更真实数据库。

## 2026-11-09 — 1B 账号认证（待验证，代码已落盘）

- 永久账号注册、登录、退出、改密、本人资料与管理员初始化的实现已落盘。
- 新增 persons、users、person_accounts、student_no_history、auth_sessions、audit_logs 六表迁移。学号属于身份、可空唯一；历史外键限制删除。
- JWT 使用 HttpOnly Cookie；持久化会话撤销、版本失效、CSRF、来源校验、Redis 双维限流及关键事务审计。
- 登录、退出、改密统一按用户到会话顺序加锁；改密复核版本与会话并撤销全部旧会话。禁止自动提权或覆盖已有管理员密码。
- 提供首页、注册、登录、个人中心，以及对接真实 API 的加载/失败状态。未实现判题或比赛的伪接口。
- 已编写认证全流程、CSRF、JWT、事务回滚、禁用/改密失效、限流和管理员保护测试；这些依赖外部 Go 模块的测试尚未实际执行。
- 严格校验 JSON 对象、未知字段和体积；退出接口允许无请求体或 JSON 空对象，新增十种输入边界测试。前端密码长度统一按 UTF-8 字节计算，避免浏览器字符数与服务端口径不一致。

## 2026-11-09 — 本批验证记录

执行环境：Windows/amd64、Go 1.25.4。Go 检查使用工作区 GOCACHE；无外部依赖的检查可在 GOPROXY=off 下执行。

| 检查 | 实际结果 |
| --- | --- |
| go test ./internal/config | 通过 |
| go test -race ./internal/config | 通过 |
| go vet ./internal/config | 通过 |
| go test ./webui | 通过，含页面、静态资源、404/405 和 HEAD |
| go test -race ./webui | 通过 |
| go vet ./webui | 通过 |
| go test ./migrations | 通过编译；该包没有测试用例，不代表 SQL 已执行 |
| node --check webui/static/app.js | 通过 |
| gofmt -l cmd internal migrations webui | 无未格式化文件 |
| Go parser 检查全部源码 | 通过语法检查，不代表类型检查或构建成功 |
| Compose config --quiet（仅临时进程验证值，无服务启动） | 通过；Docker 用户配置读取出现权限警告 |
| go mod download | 未成功：模块代理网络连接被当前环境阻止 |
| go test ./...、go vet ./...、go build ./... | 未通过初始化：缺少依赖与 go.sum，尚未进入编译 |
| 真实 MySQL/Redis、迁移约束、容器构建启动、浏览器联调、Linux/arm64 验收 | 未运行，按约定暂缓 |

直接依赖固定版本及三个关键第三方 API 已核验官方 tag 源码存在、声明的 Go 版本兼容本机；此核验不代替构建。

下一批入口：先在可获取依赖的环境执行 go mod tidy，保留 go.mod 间接依赖和 go.sum；完成全量 test/vet/build/race 并修复失败，再验收真实 MySQL/Redis 与开发 Compose。首批尚未标记完成，不提前宣称完整 OJ 或生产部署可用。

## 2026-11-09 — 继续验证与 2A 准备（进行中）

- 再次确认 Go 直连模块代理被限制，Docker 引擎仍不可用。
- 找到正常权限的直接 curl HTTPS GET 下载路径；从官方模块代理与 sum.golang.org 校验记录补齐工作区缓存和真实 go.sum，未关闭模块校验。HEAD 请求失败不能用于断言 GET 不可用。
- 修复原始 JSON 非法 UTF-8 会被 encoding/json 替换后进入密码验证的问题，新增输入回归和 HTTP 改密/清 Cookie/旧会话失效用例。
- 计划新增独立的内存测试数据 ZIP 安全校验模块（2A）；格式、限制、分值与摘要规则先写入实施计划。无数据库/API 变更，不执行用户代码。
- 实际全量验证及 2A 测试结果待完成后补记。

## 2026-11-09 — 首批本地验证完成

- 使用官方模块包和官方校验记录补齐缓存，保留原 10 个直接依赖版本，由 Go 自动添加 7 个间接依赖。go.sum 已生成；未关闭 GOSUMDB，未手写 ziphash。
- go mod tidy：通过。go mod verify：通过，输出 all modules verified。
- go test -mod=mod ./...：通过，认证、服务、存储、配置、页面均实际执行；两个命令入口成功编译。此时 2A 测试尚未完成，不将其列入该结果。
- go vet -mod=readonly ./...、go build -mod=readonly ./...：通过。
- go test -race -mod=readonly ./internal/auth ./internal/server ./internal/storage ./internal/config ./webui：通过。
- Web 与 ojctl 均成功交叉编译为 Linux/amd64 和 Linux/arm64；只验证产物构建，没有声称在 Linux 或 arm64 上运行成功。
- 实际启动临时 Web 进程，指向无数据库/Redis 服务的测试端口：GET /health=200、/ready=503、/register=200、/api/auth/csrf=200 且返回 token；HTTP smoke 通过。测试进程已关闭，不将强制关闭计作优雅退出验收。
- Dockerfile 改为复制已锁定的 go.mod/go.sum 并使用 -mod=readonly 构建，构建时不再隐式执行 tidy 修改依赖；README 同步更新。
- 当前本地检查使用 GOCACHE=.cache/gobuild、GOMODCACHE=.cache/gomod、GOPROXY=file:///D:/Code/biue/.cache/gomod/cache/download；缓存不进入源码或容器上下文。
- 真实 MySQL/Redis 的迁移、唯一约束/锁与故障恢复、Docker 镜像构建/启动、浏览器联调和 Linux 部署仍待验。

## 2026-11-09 — 2A 测试数据 ZIP 校验器（已完成独立模块验收）

- 新增 internal/testcase：在内存中解析 ZIP32，返回成对输入/输出、精确分值映射、总分/总字节数及稳定 SHA-256 内容摘要。
- 在标准库分配 ZIP 条目前限制中央目录大小和实际条目数量；拒绝危险路径/名称、链接、加密、多卷、ZIP64、未知成员、缺失配对及大小写冲突。
- 限制压缩包、单成员与总展开体积，校验声明尺寸与实际读取量，支持上下文取消，失败不返回部分数据。保留输入输出的原始二进制字节。
- 独立审阅发现标准库在无 data descriptor 且 CRC 为零时的校验边界；模块改为始终显式验证实际 CRC，并新增对应回归。摘要完成后再次检查取消状态。
- 无新增数据库迁移或 HTTP 接口；不写磁盘、不执行代码、不生成 data_version。详细格式及调用方法见 docs/TEST_DATA.md。
- go test ./internal/testcase、go test -race ./internal/testcase、go vet ./internal/testcase：通过。
- 5 秒双 worker fuzz：通过，378,135 次执行；常规单测包含 fuzz 种子回归。
- 合并后最终 go test -mod=readonly ./...、go test -race -mod=readonly ./...、go vet -mod=readonly ./...、go build -mod=readonly ./...：全部通过。
- 最终 go mod verify：all modules verified；gofmt -l 无输出。
- 后续入口为 2B 题目与不可变测试数据版本管理：先细化数据库/API/权限/存储与失败补偿，再接入本模块。真实服务与部署验收仍按前述待验证项保留。

## 2026-11-09 — GitHub 仓库导入准备（待验证/待推送）

- 用户指定目标仓库 https://github.com/Half-life7/OJ，授权提交当前项目。
- 已初始化本地 Git 的 main 分支；未修改既有源码功能或依赖。
- 发布前只读审核未发现真实 .env、私钥或常见访问令牌；仅有空白密码/密钥的 deploy/.env.example。忽略规则排除缓存、环境配置、数据目录和可执行产物。
- 当前任务对 .git 的写入受限，设置远端和 git add --dry-run 均无法创建锁文件；没有生成提交，也没有执行成功的 push。
- 现有 GitHub 凭据管理器无法访问/持久化 wincredman 凭据，远端读取未通过认证。不能将匿名 API 的 404 解释为仓库不存在。
- 准备 .cache/OJ-source.zip 及文件摘要清单，作为可审阅的完整源码包；不包含 .git/.cache、真实环境配置或构建缓存。
- 后续须在 Git 元数据可写且 GitHub 已认证的环境中提交并推送；若远端已有提交，先获取并合并历史，禁止强制覆盖。
