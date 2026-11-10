# 批次 6：P2 扩展

状态：已完成（2026-11-10），迁移版本 **9**。原始需求为 plan.md 的 P2 六项；本批具体选择如下，不新增第四个全局榜。原生整机待验事项沿用此前约定。

1. SSE：认证后推送提交摘要快照，断线重新读取当前状态，每次推送重查会话与权限，不推送源码、隐藏数据或运行输出。短连接有界，终态关闭，前端保留轮询降级。
2. Special Judge：题目不可变题面版本保存 Python 判题源码，只允许超级管理员维护；公开题目与比赛题面不暴露源码。选手程序结束后，独立受限 Python 沙箱接收 input/expected/output 的 base64 JSON，标准输出只能为 AC 或 WA。判题器异常转为基础设施失败并按现有预算重试，诊断不含隐藏数据。
3. 重判：超级管理员可对终态提交按 expected generation 发起最多 100 条原子批量重判；请求幂等键绑定操作者及请求内容。增加 generation、保留旧结果、固定原题面/数据版本并写 Outbox；禁止已结算比赛、拒绝在途提交，冻结榜单仍保持原快照。
4. 排行榜统计：现有三个榜增加 all/week/month（全部/滚动 7 天/30 天）维度，沿用身份去重和可重建 30 秒快照。
5. 题型：支持 programming 与 output_only；后者用 JSON 对象提交 case_id → UTF-8 输出，不执行选手程序，同样逐点计分并可使用 SPJ。
6. 语言：增加固定版本 Node.js 22 JavaScript 沙箱，保持原四语言行为与隔离策略。

SPJ 限制：源码最多 64 KiB，单点输入与答案总和最多 4 MiB，选手输出最多 4 MiB，判题沙箱 1 秒/256 MiB；固定 Python 执行方式、禁网、只读根、独立 tmpfs、PID/CPU/输出限制，不接受自定义宿主命令或镜像。

验收包括重判幂等/冲突/回滚/历史与榜重建、已结算保护、SSE 权限撤销与断线/终态、SPJ AC/WA/异常/答案隔离、输出型题目、JavaScript 真实运行、周/月去重及缓存恢复，另做浏览器和原功能回归。原生整机验收延续此前约定，单列待验。

## SSE 使用和边界

`GET /api/submissions/:id/events` 要求有效会话与本人/超级管理员/所属比赛管理员权限。每秒重查会话和对象权限，每用户最多 2 个流，每进程最多 32 个；单连接最多 10 秒，不放宽现有 HTTP 超时。事件为 snapshot、end、unauthorized、unavailable；snapshot 只含 Summary，不包含源码、stdin/output、隐藏测试数据或诊断。id 由 generation/status/updated_at 组成，Last-Event-ID 相同时跳过重复快照；不提供持久事件回放。

Nginx 对事件路径关闭代理缓冲和缓存，服务端设置 X-Accel-Buffering=no。该头可能被代理消费，验收用实际事件到达时间验证即时送达。页面收到快照后按权限读取完整本人详情，终态关闭；连接错误降级为已有轮询。页面离开关闭流并取消请求。终态后若另有管理员重判，重新打开/刷新详情可读取新 generation。

## SPJ 配置与协议

超级管理员在 `/admin/problems` 配置 problem_type=programming/output_only、checker_mode=tokens/spj、checker_source。tokens 模式的 source 必须为空；spj 模式必须有有效 UTF-8 Python 源码。公开 API 删除 checker_source，比赛题面即使由比赛管理员读取也不返回它；全局题目管理员接口才提供源码编辑。修改会创建新的不可变题面版本，比赛与已有提交仍绑定原版本。

每点独立执行判题器，stdin 是如下 JSON，三个值均为标准 base64 编码的原始字节：

```json
{"input":"MiAzCg==","expected":"NQo=","output":"NQo="}
```

示例判题器（不使用宿主命令）：

```python
import base64, json, sys
data = json.load(sys.stdin)
expected = base64.b64decode(data["expected"])
actual = base64.b64decode(data["output"])
try:
    print("AC" if abs(float(expected) - float(actual)) < 1e-6 else "WA")
except ValueError:
    print("WA")
```

必须正常退出且 stdout.strip() 为 AC/WA；超时、内存/输出超限、异常退出或其他输出都按判题基础设施故障重试，最终 SystemError/死信。不会把判题器 stdout/stderr 或答案放入提交结果/日志。判题器资源不计入选手的运行时间和内存；输出型选手资源为零。管理员可先用公开样例验证代码，生产诊断只给通用错误。

原始四语言的 tokens 比较行为保留。新增 JavaScript 使用 [官方 Node.js](https://github.com/docker-library/official-images/blob/master/library/node) 的 `node:22.23.4-bookworm-slim`，编译步骤为空，执行固定 node 命令。和原有语言一样经过签名监督程序、禁网/只读根/非 root/cap-drop/PID/内存/CPU/输出限制；Web/Worker 没有 Docker Socket。编辑器在没有对应语法模块时使用原有纯文本降级。

## 输出型提交

题目 problem_type=output_only 时只接受 Submit + language=text。题目详情返回只读 output_cases 标识清单，比赛使用固定数据版本的清单；不返回上传测试文件的输入或答案。管理员须在题面提供离线任务所需的公开信息。source_code 是 case_id 到 UTF-8 输出的 JSON 对象字符串，最多 64 KiB；重复/未知 case ID、非字符串值和格式错误返回 400，缺失测试点判 WA。样例正文：

```json
{"a":"5\n","b":"11\n"}
```

页面只给出输出型选项并禁用 Run。提交仍使用原有任务、Outbox、租约、结果原子保存和 ACK；Runner 不启动选手容器，直接逐点比较输出或启动专用 SPJ。不能通过 text 语言绕过程序题执行，也不能为输出型题目选择代码语言。

## 原子重判

`POST /api/admin/operations/rejudge`：

```json
{"key":"operator_request_001","reason":"重建成绩","items":[{"id":12,"generation":1},{"id":15,"generation":2}]}
```

永久超级管理员才可调用；key 为 8～64 位字母/数字/下划线/连字符，items 最多 100 个不重复 ID，reason 必填。只接受当前终态和匹配 generation，检查队列容量；同一事务创建 generation+1 的任务和 Outbox、重置当前统计、保存幂等回执/审计并清除全局榜缓存。任一项冲突则整批回滚。事务提交结果未知时可用完全相同 key/内容重试；相同 key 不同内容返回 409。

保留旧 submission_results、逐点结果及任务，已有数据版本/源码/题面版本不变。修订 checker 只影响新提交，不把旧任务偷偷改成新规则；要执行新规则应基于新版本创建新提交。比赛未结算时实时榜从当前轮次结果重建，封榜快照保持；比赛已结算拒绝重判，不重复入账。后台可填写编号:轮次列表，详情显示当前轮次并可访问新任务。普通死信重试沿用既有路径。

## 三榜周期

三个 `/api/leaderboards/:kind` 接口新增 period=all/week/month，默认 all。week/month 分别按 UTC 滚动 7/30 天筛选有效事实首次达到时间，再按 person 聚合；重复 AC/同题多题解不会产生新的覆盖数。比赛按已结算时间筛选。快照按 `(kind,period)` 独立缓存，30 秒到期或内容/身份/结算/重判变更后可重建。仍只有 practice/contest/solutions 三个榜。

## 验收记录

全量 Go test/race、vet/build、原 16 项 Node 行为测试通过；新增覆盖输出 JSON/配置边界、虚拟语言无容器执行、重判事务/审计失败回滚、幂等冲突和已结算保护、SSE 摘要保密/容量，以及周期白名单。实际 MySQL 迁移 9 和不可安全降级保护通过，拒绝把 SPJ 快照退回按 tokens 解释的旧版本，不改变当前 schema/dirty 状态。

`python tools/check_extensions.py` 使用真实 Node.js Submit/Run、Python SPJ AC/WA/异常重试和输出型提交；验证 SSE 即时送达/终态/撤权、公开及比赛题面源码隐藏、重判原版本/旧结果/重复键/原子失败/全局缓存重建、已结算保护、周/月统计。`node tools/check_extensions_browser.cjs` 验证输出型编辑器、实际 SSE、新轮次、周期切换及私有 SPJ 编辑器，桌面/390px 手机视口无 JavaScript 异常，截图在 `.cache/6-extensions-desktop.png`、`.cache/6-extensions-mobile.png`。

本批应用和新增 Node.js 镜像完成 arm64 构建，Node 版本/签名监督程序仿真启动通过，arm64 Web 的 /ready 实际连接迁移 9 数据库和 Redis。测试使用独立标签，不替换 amd64 运行镜像；不将仿真记为原生整机验收。部署仍使用既有 install/update 命令，新 runner 启动依赖 Node 镜像；部署前备份并运行 migrate up，当前 ExpectedVersion=9。回退脚本拒绝已有 SPJ/输出型快照或 JS/text 提交的降级，不删除历史绕过校验。
