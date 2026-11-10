# 批次 4：日常业务与三个全局榜

状态：已完成（2026-11-10），当前迁移版本为 **7**。需求基线为 plan.md 第十至十二章。

- 保留既有日常 Run/Submit、搜索、难度/标签过滤和本人历史；增加分类、题目通过人数/通过率及个人通过状态。
- 题单归永久账号所有，支持公开/私有、编辑/软删除、显式有序题目数组和收藏；非公开题目不因题单而泄漏。修改使用 revision 防止并发覆盖。
- 讨论支持公开题目与比赛上下文、评论/回复和幂等点赞；每次读取和写入均检查上下文权限。比赛内容仅成员或授权管理员可读，管理员可删除违规内容，父内容删除后不通过回复泄漏正文。
- 题解创建和编辑都检查作者当前有效 AC；公开内容必须经过超级管理员审核，正文或可见性改变后重新审核。作者可读私有/待审题解，公开列表只展示已通过审核且对应题目仍公开的有效内容。Markdown 经服务端渲染及净化，禁用原始 HTML。
- 固定三个全局榜：日常不同 AC 题数、已结算比赛积分、公开有效题解覆盖题数。Run/比赛提交/临时账号不进入日常榜；同一身份的多账号及同题内容去重。榜单按值降序，再按达到时间升序和 person_id 稳定排序，使用竞赛并列名次。
- 30 秒 MySQL 快照是可丢弃缓存；以数据库中的当前 generation 结果、结算账本与题解重建，缓存失效或清空后可恢复。内容修改和身份关联主动使相关缓存失效。
- 所有写入在事务内重查账号、对象权限、revision、上下文可见性，并记录审计。删除保留原始记录，公开 API 不返回已删除正文；正文有界，写入按用户限速。

验收覆盖隐私与越权、排序和冲突、收藏/点赞幂等、回复跨上下文拒绝、审核与公开撤销、Markdown XSS、三榜去重和重建、真实 MySQL/Redis/沙箱日常提交及浏览器操作。

## 页面和接口

`/learning` 为题单、讨论和题解的统一入口，按类型/题目/比赛筛选，可切换我的内容、收藏和待审队列。`/rankings` 固定提供三个全局榜，按 50 条分页并每 30 秒刷新。题目页提供日常通过人数、通过率、本人通过状态及讨论/题解入口。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/api/community` | kind=list/discussion/solution；after 游标；problem_id/contest_id/parent_id；mine/favorites/review=1 |
| GET | `/api/community/:id` | 受权限保护的正文、安全 HTML、题单有序题目和计数 |
| POST / PUT | `/api/community` / `/api/community/:id` | 创建/编辑，编辑必须携带 revision |
| POST | `/api/community/:id/actions` | action、revision；delete/approve/reject/favorite/unfavorite/like/unlike |
| GET | `/api/practice/problems/:id` | submissions、accepted、people、acceptance_rate（0～1）、solved |
| GET | `/api/leaderboards/:kind` | practice/contest/solutions，page 从 1 开始；含快照时间和总人数 |

创建/编辑字段为 kind、title、body、visibility、可空 problem_id/contest_id/parent_id，以及题单的 problems 数字 ID 数组。题单数组次序即展示次序，不允许重复；上下文在创建后不可转移。讨论回复继承上下文和可见性，最多 16 层。删父内容会隐藏其后代，原文保留以便审计；公开读取不返回删除正文。

匿名可以读公开题单、公开题目讨论、审核通过且仍有有效 AC 的公开题解和三榜。比赛讨论仅成员和授权管理员可见，临时账号可读取本场比赛讨论但不能发布日常社区内容。只有永久账号可写入；作者可编辑/删除，超级管理员可审核题解和删除违规内容，比赛管理员只能删除授权比赛中的违规讨论。无权读取的对象统一返回 404；修订冲突 409，缺少 AC 或操作越权 403，限速 429。全部写接口继承会话、CSRF 和 Origin 校验。

标题最多 200 字符，正文最多 64 KiB，题单最多 200 道题，JSON 最多 128 KiB。成功写入每账号每分钟最多 30 次，计数在 MySQL 事务中保护；失败事务不产生孤立内容、收藏或点赞记录。Markdown 使用 [Goldmark](https://github.com/yuin/goldmark) 默认禁用原始 HTML，再经 [Bluemonday](https://github.com/microcosm-cc/bluemonday) 白名单净化，代码块保留语言 class。

## 日常统计和三榜口径

- 分类复用现有标签体系：`category:graph` 表示 graph 分类，题库接口的 `category=graph` 可与 q、difficulty、tag 同时使用。分类名最多 23 字符；管理题目的标签字段即可设置。
- 日常通过率统计已完成且与当前 generation 结果一致的日常 Submit；CE 计入提交数，SystemError/Cancelled 和未完成任务不计入分母。AC 提交数/有效提交数为通过率，通过人数按当前 person 去重。Run 和比赛提交始终排除。
- 三榜均只展示当前 active 的永久账号身份，显示其字典序最小的账号 ID。日常榜按 person/题目去重；题解榜只计公开、审核通过、未删除、题目公开且作者仍有有效 AC 的 person/题目。
- 比赛榜从已结算积分及成员关系汇总，按当前关联 person 和每场比赛取最高积分，再对比赛求和；正式有效队伍才计入。身份关联可合并多个账号的既有比赛记录，不能把同场积分相加。
- 并列以数值和达到时间相同为准，采用 1、1、3 竞赛排名；最后以 person_id 稳定展示。日常达到时间为各题首次有效 AC 的最大值，题解为各题最早有效公开审核时间的最大值，比赛为计入比赛的最后结算时间。
- global_board_cache 是默认 30 秒的惰性 MySQL 快照，内容写入、身份关联和结算会主动清空。判题完成/重判和题目状态变化最迟在缓存到期后反映；浏览器每 30 秒重读。缓存可删除后直接重建，Redis 没有唯一成绩副本。

## 验收记录与复现

全量 `go test ./...`、`go vet ./...`、`go build ./...`、`go test -race ./...`、`go mod verify` 通过；既有题库/提交页面 16 项 Node 行为测试通过。安全测试包含 raw HTML、事件属性、危险 URL、SVG、代码块转义及并发渲染。

独立 Docker 项目 `biue-batch3` 已升级至迁移 7，保留旧数据及独立卷。`python tools/check_community.py` 使用真实 Python 沙箱日常 AC，验证重复 AC/Run 排除、分类/通过率、私有题单与有序编辑、冲突、收藏/点赞幂等、跨上下文回复拒绝、父删除后回复不可见、比赛讨论权限、题解 AC 前置/审核/修改后重审/撤销、比赛打星退赛排除、多账号积分去重和删除快照后重建。

`node tools/check_community_browser.cjs` 使用实际 Edge 验证桌面和 390px 手机视口，涵盖题单创建、Markdown 安全、排序编辑、收藏、三榜切换、题目统计及分类恢复，无 JavaScript 异常。截图位于 `.cache/4-learning-desktop.png`、`.cache/4-learning-mobile.png`。

脚本沿用批次 3 的独立 Compose 环境和 Git 排除的 `.cache/2d-acceptance.env`，需初始化验收管理员（BIUE_SMOKE_PASSWORD）并启动 Worker/Runner。浏览器还需已安装 Windows Edge 与 `.cache/browser` 中的 Playwright。脚本创建唯一测试账号与内容，不清空数据库；只删除可重建的全局榜快照。没有进行生产容量或长期压力测试，相关事项归部署批次。
