# 题目与不可变测试数据版本（2B）

本模块在 Gin HTTP 层接入题库、超级管理员题目管理和 ZIP 校验器。题目提交与判题队列已由 2C/2D 接入，比赛授权读取属于后续批次。实现进度与实际验证结果见 CHANGELOG.md。

## 权限与题面

- 匿名访问和普通用户只读取 `published` 且 `public` 的题目。隐藏、草稿、下架和 `contest_only` 题目返回 404；后者的比赛访问将在比赛模块接入。
- `/api/admin/problems` 下的全部接口先校验数据库会话及 `super_admin` 权限；前端隐藏控件不能代替后端鉴权。
- 题目包含标题、Markdown 描述、Markdown 输入/输出格式、纯文本样例、时间/内存限制、难度、标签、状态、可见性、创建人与时间。公开响应同时返回经过禁用原始 HTML 和 Bluemonday 净化的 `description_html`、`input_html`、`output_html`；编辑器保存 Markdown 原文。样例仍保留原始换行。
- 状态为 `draft/published/archived`，可见性为 `public/hidden/contest_only`，难度为 `easy/medium/hard`。新题默认草稿和隐藏；选择公开可见时服务端同时设为已发布，避免“看似公开但仍在草稿”的歧义。下架使用 archived，保留所有历史，不提供物理删除。
- 标题最多 200 个字符，描述最多 64 KiB，输入/输出格式和样例各最多 16 KiB，标签最多 16 个、每个最多 32 个字符。时间限制为 1～60000 ms，内存限制为 16～4096 MiB。题目 JSON 请求体上限 256 KiB，拒绝未知字段、非法 UTF-8 和多份 JSON。

## API 与并发编辑

全部响应沿用 `{code,message,data}`；管理写请求需要 CSRF Cookie 和 `X-CSRF-Token`。分页默认 page=1、limit=20，limit 最大 100。

题目管理页使用独立工作区：左侧导航在“题目列表、创建题目、编辑当前题目、上传测试数据、数据版本”之间切换，只显示一个右侧面板。URL hash 保留 `list/new/edit/data/versions` 与当前题目 ID；从列表选择题目后进入编辑工作区，不再把编辑器接在列表下方。同一模式用于比赛中心列表/创建和身份列表/编辑/关联。题面或数据版本保存/发布后会自动调用单题范围重判，保存与重判结果分别展示。

| 方法与路径 | 用途 |
| --- | --- |
| GET /api/problems | 公开列表，支持 q、difficulty、tag 及分页 |
| GET /api/problems/:id | 公开题面与样例，不返回隐藏测试数据 |
| GET /api/admin/problems | 管理列表，增加 status、visibility 过滤 |
| POST /api/admin/problems | 创建题目和首个不可变题面版本 |
| GET /api/admin/problems/:id | 管理详情，包含 revision 与当前版本号 |
| PUT /api/admin/problems/:id | 完整更新题面和配置，必须携带当前 revision |
| POST /api/admin/problems/:id/data-versions | ZIP 或多个文件导入，必须携带当前 revision 和 replace/merge 模式 |
| GET /api/admin/problems/:id/data-versions | 数据版本摘要列表及分页 |
| GET /api/admin/problems/:id/data-versions/:version | 单版本摘要及测试点分值/长度元数据 |
| POST /api/admin/problems/markdown-preview | 超级管理员安全预览 Markdown，返回净化后的 HTML |
| POST /api/admin/problems/package-preview | 预览内置或上传题包，不创建数据库记录 |
| POST /api/admin/problems/package-import | 完整校验后原子创建题目、题面版本和数据版本 |

题目请求字段为 title、description、input_format、output_format、sample_input、sample_output、time_limit_ms、memory_limit_mb、difficulty、tags、status、visibility。更新额外携带 revision；创建时省略 revision 或设为 0。列表 data 为 `{items,page,limit,total}`。题目返回 id、revision、statement_version、data_version、creator_id、created_at、updated_at 等字段；尚无测试数据时 data_version=0。

编辑和导入在数据库行锁内检查 revision，过期修订返回 409，不能静默覆盖他人修改。题面更新增加 statement_version 和 revision；导入增加 data_version 和 revision。已保存题面快照和测试数据版本始终保留，为后续比赛与提交绑定稳定版本提供基础。`public` 与 `published` 的最终状态只由服务端确认，前端显示不授予权限。

## ZIP 或直接文件导入与发布

- 使用 multipart/form-data，可提交一个 archive 字段，或重复提交 files 字段选择多个 `.in/.out` 文件；必须包含一个 scores JSON、revision 和 mode。ZIP 上限 32 MiB；每个直接文件上限 16 MiB，合计上限 64 MiB，整个请求最多 66 MiB。ZIP 经过 ZIP32/CRC/路径/展开预算检查，直接文件经过同一名称、配对、大小和分值检查。
- mode=`replace` 发布完整新数据集；mode=`merge` 在行锁内读取当前不可变版本，按测试点 ID 替换已上传项并保留其他旧项，然后发布完整新版本。合并输入和分值只包含本次上传项；当前旧内容不会发送给浏览器。
- scores 形如 `{"1":40,"2":60}`，精确覆盖本次 ZIP 或直接文件中的测试点，支持零分点和总计非零。页面从文件名生成表格，可单点编辑、批量赋分或复制首项，并显示点数与总分。支持重传；即使内容相同，每次成功发布仍产生新的 data_version。
- 页面使用 XMLHttpRequest 展示实际上传进度并提供取消。上传或取消后若客户端未收到响应，会重新读取题目并仅当 revision/data_version 恰好前进一次时确认发布；无法判断时提示核对版本，不自动重试。连接、Nginx、Web 与请求上下文为上传使用独立且有限的 120 秒预算，其他 JSON API 仍保持短超时。
- 进程内限制同时导入数（PROBLEM_IMPORT_CONCURRENCY，默认 2，范围 1～8）；满额返回 429。超大归档/请求返回 413，无效格式或分值返回 400，存储/数据库故障返回 503。
- 文件写入 PROBLEM_DATA_DIR（默认 oj-data/problem-data），使用服务器生成的随机存储键和受控文件名。ZIP 原始文件名不参与目录构造；先完整写入暂存目录并关闭文件，再原子发布，最后在 MySQL 同一事务登记版本、测试点元数据、当前指针与审计。
- 导入失败保持原有数据库指针。已发布但未被数据库引用的目录可以成为孤儿；尤其 Commit 返回错误时可能实际已提交，不能据此删除目录。本批不做自动孤儿清理，后续清理须核对数据库引用并避免与在途上传竞争。
- 管理接口只返回摘要、分值和长度；任何题库 API 均不返回隐藏输入、答案、存储键或服务器路径。物理目录不挂到静态资源服务。

Compose 为 Web 挂载专用持久卷，保留只读根文件系统，Worker 只读挂载相同数据。当前完整构建需迁移到版本 9；数据库、problem-data 与 runner-data 均须备份。2D 已实际验证题目创建/导入、不可变版本在编辑/重传后的保持及判题恢复；生产备份恢复和压力验证另列后续。

SPJ 编辑区提供最小 Python 模板、本地 `.py` 导入、样例输入/预期输出预览和保存后的真实提交入口。保存 checker 会创建新题面版本；普通提交仍由独立 Python 沙箱执行实际 checker，旧提交、比赛固定快照和人工重判继续使用各自版本，不存在在保存时改写旧结果的行为。

## 编辑器与 Markdown

- 题目描述和输入/输出格式使用本地 CodeMirror 6 Markdown 编辑器，带语法高亮、四空格缩进、`Tab`/`Shift+Tab`、`Ctrl/Cmd+/`、粗体、斜体、链接、行内代码、代码块、标题、列表、引用和 `Ctrl/Cmd+Enter` 安全预览。普通提交、比赛提交和 SPJ Python 复用同一代码编辑器层。
- Markdown 预览通过 `POST /api/admin/problems/markdown-preview`，该接口在服务端使用与社区相同的 Goldmark 渲染及 Bluemonday 净化。前端不会把未净化预览直接写入页面；编辑区失败时回到普通 textarea。

## 外部题包导入

内置样例位于仓库根目录 `导入样例/`，当前包含 `problem.yaml`、`problem_zh.md`、`testdata/config.yaml`、`testdata/1.in` 和 `testdata/1.out`。运行时默认读取 `PROBLEM_SAMPLE_DIR=导入样例`，镜像会把该目录复制到 `/app/导入样例`。

- 管理页测试中心先调用 `package-preview` 展示标题、Markdown 摘要、测试点、字节数和总分；只有显式点击“确认导入”才调用 `package-import`。
- 上传题包必须使用同样结构。允许 ZIP 内只有一个包装根目录（例如用户样例的 `23/`），解析前会安全剥离；仍拒绝多根混合、路径穿越、符号链接和未知文件。`problem.yaml` 至少包含 `title`，可带 `tag`、难度、时间/内存；外部站点的 `pid`、`owner`、`nSubmit`、`nAccept` 等统计被忽略。Markdown 可识别题目描述、输入、输出和 `input1/output1` 样例代码块；无法识别分区时完整原文仍保留在描述中并显示警告。
- `testdata/config.yaml` 读取 `time`/`memory` 简写或 `time_limit_ms`/`memory_limit_mb`，并读取 subtasks/cases/score/input/output。未提供 subtasks 时，全部成对测试文件按文件名自然顺序平均分配 100 分，余数给最后一点并显示警告；持久化数据集仍按系统约定的 ID 排序。每个 case 可显式覆盖 score；未覆盖时子任务分值在 cases 间均分。测试文件继续使用 ID、配对、文件/总字节、ZIP32、CRC、路径和总分校验，生成不可变摘要。
- 单题包 ZIP 最多 32 MiB，测试点上限沿用 512 对；拒绝未知根文件、路径穿越、符号链接、ZIP64、重复成员、无引用测试文件和超限。导入在完整解析后原子创建题目/题面/数据/指针/标签/审计；失败不留下可见半成品。隐藏测试内容不返回浏览器。
- 重复点击由客户端禁用，成功后将按钮改为打开本次题目；该保护只在当前页面有效，后端没有跨设备全局包导入幂等键。
