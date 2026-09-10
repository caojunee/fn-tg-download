# 版本记录

## v0.5.4（2026-09-10，feature/resource-picker 分支）

- **「下载选中」= 立即下载**：资源选择器选中资源后点「下载选中」直接创建任务并开始下载，不用再回页面点「开始下载」。创建期间按钮 loading（"正在创建…"）、取消按钮禁用、弹窗不可关闭；成功后自动把该链接从输入框与解析结果中摘除（避免再点「开始下载」时重复建任务），待下载列表清空时跳转任务页；失败则保留弹窗与已勾选范围，可重试或用「开始下载」兜底（勾选范围仍会带上）。
- 「开始下载」语义不变：创建输入框里剩下的链接（未手动选资源的链接 / 整频道 / 单条消息），带上前述兜底的勾选范围。
- **修复：资源较多时卡片被压扁、文件信息显示不全且无滚动条**：资源网格 `grid-auto-rows` 为默认 `auto`，会被压缩到弹层可用高度内（实测 6 条 @ 矮窗口时行高被压到 43px、200 条时压到 2px），卡片内容（缩略图/文件名/大小）被 `overflow:hidden` 裁掉，且因为不产生溢出所以没有滚动条。改为 `grid-auto-rows: max-content`（行高固定为内容高度）+ `overscroll-behavior: contain`，并在网格上加 6px 细纵向滚动条（`::-webkit-scrollbar`；Firefox 用 `@supports not selector(::-webkit-scrollbar)` 兜底 `scrollbar-width: thin`，因为 Chrome 一旦设置 `scrollbar-width` 就会忽略 webkit 伪元素）。实测行高恢复 155px、200 条可滚 8476px 且末条可达。
- 资源选择器改动仅前端（`NewDownloadView.vue`、`ResourcePicker.vue`），复用现有 `selected` 参数。
- **修复：清空下载历史后解析链接仍显示"已下载"**：链接台账 `download_urls` 表此前独立于 `tasks` 表、只增不删（v0.5.0 设计为"清历史不删"），导致清空历史后重新解析同一链接仍带"已下载"徽标。现在台账生命周期跟随任务记录——`ClearHistory` 清空台账（保留活跃任务仍在用的 URL，顺带清掉修复前遗留的孤儿行），`Remove(ids)` 同步删除被删任务 URL 的台账行（仍被其他任务引用的不动）。`Persister` 接口新增 `DeleteURLs` / `DeleteURLsExcept`（MySQL 实现，JSON 兜底不支持则不启用台账）。
- **修复：serve 中断导致任务被判失败（进度丢弃）**：上游断流/tdl serve 异常退出时 aria2 报 `Got EOF from the server`，此前是 3 次固定 2s 重试（约 10s）就整任务 failed（实测 1.1GB 任务停在 94.5%）。现在：① 下载循环用 `ServeProc.Alive()` 非阻塞探测 serve 退出，退出即重建 serve（端口会变，句柄同步），aria2 凭 `.aria2` 控制文件字节级续传；② 启动 serve 失败重试 3 次；③ aria2 重试 5 次、退避 2/4/8/16s。实测下载中 `kill -9` serve 进程 6s 后自动重启并续传至完成。

## v0.5.3（2026-09-06，feature/resource-picker 分支）

- **auth/me 登录缓存**：不再每次起 Telegram 客户端实测（bolt 锁+网络自检，下载占锁期间阻塞到超时）。内存+data/me.json 双层缓存，占锁期间直接回缓存（实测 0.004s），不占锁时实测并刷新；登录成功写缓存、退出登录清缓存。
- **保存目录对齐**：占锁期创建的任务子目录可能是链接回退名（频道 username），与磁盘断点的频道名目录对不上——任务启动迟到探测后，目录为空时按频道名纠正。
- **发版缓存根治**：index.html（含 SPA fallback）`Cache-Control: no-cache`，`/assets/*` hash 资源一年长缓存。杜绝发版后浏览器缓存旧入口导致"部署了没效果"。
- **统计卡片等高**：速率值拆「数值+小号单位」渲染，数值长时分级缩小字号（is-lg 14px / is-xl 12.5px），不再换行撑高卡片；grid `auto-rows: 1fr` 同行强制等高。桌面/手机共用视图一处生效。
- 含 v0.5.2 全部改动：下载中创建任务自动排队（0.1s 返回）、暂停/取消后调度排队任务、aria2 多连接提速（峰值 8.9MB/s）、MySQL 任务加载修复。

## v0.5.2（2026-09-06，feature/resource-picker 分支）

- **下载中创建任务不再阻塞（自动排队）**：tdl bolt 存储锁被下载中的 serve 独占，create/probe 接口同步探测会阻塞到下载结束（前端转圈到超时）。现在占锁期间 create 跳过探测立即入队返回（实测 0.1s），任务启动拿到锁后补一次迟到探测回填任务名/类型/大小；probe 立即返回排队提示。纯进程内队列（serveGate+并发槽），无外部依赖。
- **修复暂停/取消后排队任务不被拉起**：Pause/Cancel 释放并发槽后补 `pumpLocked` 调度。
- 整频道任务按链接形态（ChannelLink）兜底识别，不再依赖探测出的 kind（跳过探测时 kind 为占位值）。
- 含 v0.5.1 全部改动：aria2 多连接提速（`-x 16 -s 16 -k 4M`，serve 单流 ~700KB/s → 3MB/s+ 峰值 8.9MB/s）、MySQL 任务加载 Scan 列数修复、安装向导默认生产参数。

## v0.5.1（2026-09-06，feature/resource-picker 分支）

- **下载提速（aria2 多连接）**：tdl serve 对单个 Range 请求只走一条 Telegram 拉取流（实测 ~700KB/s 稳定，波动时低至几百 KB/s），aria2c 默认单连接正好被卡死。`Aria2Args` 增加 `-x 16 -s 16 -k 4M`（16 连接分段，serve 原生支持 Range/206），实测 3MB/s+、峰值 8.9MB/s；旧 `.aria2` 控制文件对分段参数变化兼容，恢复不重下。tdl 侧加 `-t` 无增益，不采用。
- **修复 MySQL 任务加载失败（重启后任务全丢）**：`store/mysql.go` Load() SELECT 19 列但 Scan 只接 17 个（v0.5.0 加 peer_id/selected 列时漏改），报 `expected 19 destination arguments in Scan, not 17`，整表加载失败。补扫两列并反序列化。
- **安装向导默认值改为生产参数**：DB `192.168.31.180/td_download`、代理 `http://192.168.31.180:20172`、服务端口 8062（manifest `service_port` 同步）。升级安装保留原 `etc/env` 不受影响，仅全新安装时向导预填生产值；SIT 走手动部署不经向导。

## v0.5.0（2026-09-05，feature/resource-picker 分支）

- **字节级断点续传**：下载路径重构为 `tdl dl --serve`（本地 HTTP，支持 Range/206）+ `aria2c -c`（`.aria2` 控制文件记录断点）。暂停/失败/服务重启后从断点字节处继续，不再像 tdl 内置下载器那样把半成品 `.tmp` 截断重下（tdl 续传为文件级）。
- **资源选择器**：解析链接后可打开资源选择器——缩略图网格（懒加载）、文件名/大小/日期、关键字与类型筛选、全选/反选/清空，确认后只下载勾选条目（生成过滤 JSON 走 `tdl dl -f`）。支持整频道、相册、组合消息。
- **平台自适应弹层**：`ResponsivePopup` 桌面端居中弹窗 / 移动端上拉抽屉，按平台懒加载容器组件。
- **自定义 Telegram API_ID / API_HASH**：系统管理新增可选配置，留空用内置身份，保存重启并重新登录后生效。
- **aria2c 静态二进制**打入 fpk（`bin/aria2c`，musl 静态链接无依赖），启动脚本补 chmod 与 `-aria2` 参数。
- **相册展开适配**：tdl serve 不展开 `--group`，服务端对相册链接先自行枚举展开为消息清单再走 serve。
- 下载服务（serve）独占 tdl bolt 存储锁，任务在服务端排队串行执行；逐文件下载，失败自动重试 3 次。
- 侧边栏版本号改为从后端 `/api/settings` 动态读取。

## v0.4.8（2026-09-05）

- **修复生产下载秒失败 `exit status 2`（根因确认）**：v0.4.7 日志显示 tdl 子进程 `panic: $HOME is not defined`（tdl v0.20.4 `pkg/consts` init 时要求 $HOME，缺失即 panic）。SIT 一直正常是因为此前经 SSH 会话手动启动（自带 HOME=/root），生产由 fnOS 服务环境拉起、无 HOME。
- 修复双保险：`server/main.go` 启动时 `$HOME` 为空则兜底设为 data 目录（tdl 实际存储由 `--storage` 显式指定，HOME 仅为满足其 init 检查）；`cmd/main` 启动前 `export HOME="${TRIM_PKGHOME:-/root}"`。
- 版本三处同步 0.4.8。

## v0.4.7（2026-09-05）

- **下载过程日志补全**（排障关键）：此前任务全程无服务端日志——tdl 子进程的非进度输出（错误/警告/panic 堆栈）在 `pump()` 里被直接丢弃，任务失败只显示 `exit status N`，info.log 只有启动日志。修复：
  - `download.go`：pump 剥 ANSI 后把非进度行转发 `onLog` 并存尾部缓冲（最近 10 条）；排除 tracker 进度行与渲染噪声行（CPU 统计/进度条，每 100ms 一帧，不排除会刷爆日志）。
  - `manager.go`：任务启动（含完整 tdl 命令行与保存路径）、失败（含 tdl 输出尾）、完成、导出阶段均打日志；失败时把 tdl 最后输出附加到 task.Error，前端任务卡片直接可见真实原因。
- **exit status 2 语义确认**：本地实测 tdl v0.20.4——未知 flag/未登录等错误均 exit 1；exit 2 是 Go panic 的退出码。生产「0 B / 121 MB 文件 0/2 exit status 2」疑似 tdl 子进程 panic，panic 堆栈此前被丢弃；本版日志会直接暴露堆栈尾行（`runtime error:` 等）。
- 版本三处同步 0.4.7。

## v0.4.6（2026-09-04）

- **私有链接真实探测**：probe 阶段从 tdl 的 peers 缓存（bolt，key `peers:key:channel_:<id>`，value `{"AccessHash":...}`）读取 access hash，`t.me/c/<id>/<msg>` 命中缓存即可完整探测（类型/文件数/大小/真实标题，`ChannelsGetFullChannel` 取标题）；整频道私有链接也取真实标题。未命中缓存的兜底行为不变（放行创建，前端标「私有链接 · 启动后统计」）。
- **移动端 stepper 换行修复**：窄屏下 `.opt` flex 压缩 `van-stepper`（inline-block 内容会折行）导致 +/- 换行；改 `.opt__main { flex: 1 }` + `.van-stepper { flex-shrink: 0; white-space: nowrap }`。
- 顺带发现：`go.mod` 要求 go≥1.25，本机需 `GOTOOLCHAIN=go1.25.0`（模块缓存已有 toolchain@v0.0.1-go1.25.0）。

## v0.4.5（2026-09-04）

- **单并发下载（tdl -l 2 → 1）**：tdl 内核断点续传为「文件级」（仅记录已完成文件，半成品 .tmp 恢复时截断重下，实测多并发暂停时 2 个文件同时损失进度）。改单并发后暂停/失败最多只损失 1 个文件的部分进度；已完成文件仍靠 `--continue`+`--skip-same` 秒跳过。
- 调研结论（备查）：字节级续传需 tdl `--serve`（支持 HTTP Range/206）+ aria2c -c（控制文件精确续传）改造，SIT 已预装 aria2 1.36.0，用户暂缓该方案。
- 版本三处同步 0.4.5。

## v0.4.4（2026-09-04）

- **纯文本消息拦截**（遗留边界）：probe 阶段检测消息无媒体即报错；创建入口过滤「探测失败且非私有链接」的 URL（私有链接探测失败属预期，放行由 tdl 兜底）；下载完成但 0 文件时置失败并提示，杜绝误导性的 "completed 0 文件" 空跑。
- README 功能表同步 0.4.x 特性（整频道下载、目录规则、排队占位）。
- 前端：createDownload 读取 `X-Skipped-Reasons` 响应头，部分链接被跳过时 toast 展示原因。
- 修复 X-Skipped-Reasons 响应头在写响应体之后设置导致静默失效（Go Header 须在 WriteHeader 前设置）。
- 修复多文件任务总进度提前 100%：stdout 百分比兜底取最大 tracker 值，首个单元完成即满——封顶 99，完成时置 100。
- doc/memory/architecture-decisions.md 补「下载管线（0.4.x 定型）」章节。
- 浏览器验收发现 web 构建产物停留在 v0.4.2：TS 报错（命名导入不存在的 service 导出）导致 build 失败而校验 grep 漏检。修复导入并重建，验收通过（版本/任务卡片/文件明细/移动端 tabbar）。
- 完成回填补 DownloadedSize/TotalSize：秒完成或重试的任务 watchTask 来不及累计目录差分，完成后不再显示 "0 B / 0 B"。
- 版本三处同步 0.4.4。

## v0.4.3（2026-09-04）

- **修复排队文件从列表消失**（界面 文件 0/2 而磁盘 3 个分片）：tracker 行只覆盖正在下载的单元，探测占位在下载中重建时被丢弃。修复：`syncFilesLocked` 按 expected 文件数补回「排队」占位条目（稳定 ID 防前端抖动）；expected 取首次捕获的列表长度与磁盘分片前缀数（`diskKeyCount`，tdl 预创建全部分片）的较大者，历史坏数据列表也能自愈。
- **size 磁盘自愈增强**：下载中真实名条目的 size 以磁盘字节数为准（大于旧值时覆盖），占位期均分估算的 5MB 假数据可自愈；旧条目保留路径同样处理。
- 新增 `manager_test.go` 排队补位/跨 tick 稳定用例；SIT E2E：恢复后列表 3 条、完成后 3 文件真实大小（79.9/76.6/65.4MB）。
- 版本三处同步 0.4.3。

## v0.4.2（2026-09-04）

- **修复文件列表 `.tmp` 重复条目**（暂停恢复后 2/5、size 全 0 的根因）：tdl 下载中文件名带 `.tmp` 后缀、完成后自动改名，恢复任务后新追踪器解析出最终名新条目，而旧条目（`.tmp` 名）查重按精确名字对不上 → 同一文件两条。修复：文件列表全程统一最终名（`resolveFileNames`/`syncFilesLocked`/`fillFilesFinal` 去 `.tmp` 后缀），ID/Size 跨改名保留，size 为 0 时从磁盘自愈。
- **修复恢复后完成回填丢文件**：恢复任务时 tdl 跳过已完成的同大小文件（无 tracker 行），`fillFilesFinal` 用本次追踪结果整体覆盖列表 → 只剩本次下载的文件。修复：并入旧列表中未被覆盖的真实文件条目。
- **私有频道子目录去掉「频道」前缀**：`t.me/c/<id>` 回退目录名直接用 `<id>`。
- 新增 `task/manager_test.go` 回归用例；SIT E2E：暂停恢复列表稳定 3 条无重复、完成后回填 3 文件带真实大小、新命名 `…/naming-check/4434014100` 生效。
- 版本三处同步 0.4.2。

## v0.4.1（2026-09-04）

- **补全"按频道名自动分目录"**：系统设置页文案早已承诺，但后端从未实现（任务直接下到所选目录）。
  - `Create` 时按探测标题拼子目录 `<保存路径>/<频道名>`；标题缺失时由 `DirNameForURL` 从链接回退——私有频道 `t.me/c/<id>` 用「频道 <id>」，公开频道用 username。
  - 新增 `SanitizeDirName`：剔除各文件系统非法字符 `\ / : * ? " < > |` 与控制字符、去尾点、压缩空白、限 80 rune；结果为空（如 `..`）则不分子目录。
  - 新增 `tdl/dirname_test.go`；E2E（SIT）：私有频道落盘 `e2e-subdir/频道 4434014100/`、公开频道落盘 `e2e-subdir/my love/`，取消删分片在子目录下正常。
- 版本三处同步 0.4.1。

## v0.4.0（2026-09-04）

- **新功能：整频道下载**。链接不带消息 ID（`https://t.me/c/<id>` 或 `https://t.me/<username>`）即整频道任务：
  - 探测轻量化：只解析标题（私有频道显示"频道 ID"），不再阻塞创建；真实文件数/大小在任务启动后填充。
  - 启动后先跑 `tdl chat export -c <chat> --with-content --raw`（导出文件存 `<data-dir>/exports/<taskID>.json`，暂停恢复/重试直接复用），解析出文件名+大小回填任务；再 `tdl dl -f <export> -d <dir>` 批量下载。
  - SIT 实测：`dl -f` 的 tracker 行与 `-u` 模式完全一致（`{标题}({对话ID}):{消息ID} -> {路径}~`），现有进度解析/glob/删分片逻辑全部兼容；导出 JSON 顶层 `{id, messages:[{id,file,raw}]}`，大小取 `raw.Media.Document.Size`（Photo 取 progressive 最后一档）。
  - 导出阶段可暂停/取消/删除分片；删除记录时清理导出文件；任务完成后列表回填磁盘真实文件。
  - 前端：`MediaKind` 加 `channel`，任务/探测项显示"整频道"徽标与 folder 图标。
- 新增 `tdl/export.go`（链接解析/导出子进程/导出解析）与 `export_test.go`。

## v0.3.5（2026-09-04）

- **修复"删除分片并取消"实际不删分片**（真凶终于抓到）：tdl v0.20.4 的 tracker 消息实际是「`{标题}({对话ID}):{消息ID} -> {目标路径}`」（`app/dl/progress.go` 的 `elemString`，源码+SIT 抓原始 stdout 双确认），路径过长被 go-pretty 截断成 `~` 结尾。而 0.3.4 的解析正则 `\((\d+)\):(\d+)~` 要求 `~` 紧跟消息 ID，**永远匹配不上** → `filePerc` 恒为空 → 取消时没有 keys 可删、文件列表也一直是占位（组合下载 0/1 的残余根因）。正则改为 `\((\d+)\):(\d+)(?:\s*->\s*\S+)?`，旧格式向后兼容；新增 `progress_test.go` 用实测行做回归用例。
- SIT E2E 复现路径：建任务 → 分片落盘（`4434014100_2_*.tmp`）→ cancel deletePartial=true → API ok 但分片仍在；同时 API progress 恒 0、文件名恒占位，即解析从未生效。

## v0.3.2 ~ v0.3.4（2026-09-04，补记）

- v0.3.2：tdl-data 迁官方默认位置 `TRIM_PKGHOME/.tdl/data`（用户指正应用 ~ 是 /vol1/@apphome）。
- v0.3.3：取消任务改二选一（仅取消 / 删除分片并取消，van-popup 居中 380px）；保存路径等设置项跟随 store。
- v0.3.4：重写进度解析提取 `(对话ID):(消息ID)` + 磁盘 glob 真实文件名（当时误判 tracker 行格式，实际未生效，见 v0.3.5）。

## v0.3.1（2026-09-04）

- **修复登录态始终"未登录"**：`cmd/main` 的 `-tdl-data` 少了 `/data` 后缀。官方 tdl 布局为 `<数据目录>/data/<namespace>`（session 在 `data/default`），此前 server 在 `tdl-data/` 根下自建空 bolt 导致预置登录态永远读不到。SIT 重启后 `auth/me` 恢复正常。
- **修复设置页误导文案**：原"认证信息保存在 `<下载路径>/.tdl`"是前端写死的错误提示（用户照抄把 .tdl 复制到下载目录，无效）；后端 `/api/settings` 现返回真实 `tdlDataDir` 与 `version`（构建注入 `-X main.version=`），前端改为展示后端值。`v0.x` 占位符同步消除。
- 附带：`/api/settings` 响应新增 `tdlDataDir`、`version` 字段（前端类型已加）。

## v0.3.0（2026-09-04）

- 图标更换为 `I:/my_project/logo.png`（gopher 骑纸飞机），fpk 根 ICON ×2 + app/ui/images ×4 全量替换。
- 项目迁移至 `I:/my_project/fn-tg-download`，补齐 README / AGENTS / doc/memory 文档体系。
- 打包产物：`fpk/tg-download.fpk`（36.4MB）。**注意**：`I:/tg-download.fpk` 是 0.2.9 旧包（23:38 打的），勿混淆。
- 待办：SIT 重装 0.3.0 后强刷浏览器验证新图标（桌面图标链路见 fnos-fpk-pitfalls.md#6）。

## v0.2.9（2026-09-03）

- 桌面抽屉改居中弹窗；图标统一（当时为 login__logo 规格渐变图标）；iframe 内隐藏品牌区 `sidemenu__brand`。
- SideMenu 版本号显示 `v0.3.0 · fpk` 之前的最后版本。

## v0.2.8（2026-09-03）

- 移动端适配：`<768px` 侧栏换 `van-tabbar`（新建/任务/设置/我的），操作走 `van-action-sheet`。
- 目录选择器 DirPicker（后端 `GET /api/fs/dirs`），保存路径接入。
- UserMenu popover 四项等宽修复。
- 三处前端修复均由用户实测反馈驱动（手机端不可用/目录选择不可用/popover 宽度不一致）。

## v0.2.6（2026-09-03）

- **端口同步挪到 `cmd/main` start 流程**（连 PostgreSQL `appcenter.app_service` 重试 5×2s）——根治"向导填了端口、桌面入口仍指 8080"。此前 0.2.4/0.2.5 在回调里同步，因 fnOS 注册晚于回调约 30s 而无效。
- DSN 传参加引号修复 `tcp(...)` 炸 shell。

## v0.2.2–0.2.5（2026-09-03）

- 0.2.2：`app/ui/config` 占位符硬编码（fnOS 不替换 `{display_name}/{port}`）。
- 0.2.3：桌面入口 `type:"iframe"` 内嵌打开（原 `url` 类型跳新标签）。
- 0.2.4–0.2.5：入口 URL 排查阶段版本（Playwright 实测 fnOS 桌面，发现 PostgreSQL 注册时序）。
- **0.2.1（关键修复）**：向导环境变量按原名传（无 `wizard_` 前缀）——此前 0.2.0 按 `wizard_app_port` 读全空导致端口配置失效、DB 配置失效，是多次"打不开"的主因。

## v0.2.0（2026-09-03）

- DSN 拆结构化表单（db_host/db_port/db_name/db_user/db_password）+ app_port 可配 + 启动端口预检（/dev/tcp，占用写 TRIM_TEMP_LOGFILE 提示）。
- install_callback 拼装 DSN 写 `etc/env`（sq_escape 转义）。

## v0.1.0（2026-09-03）

- 首个 fpk（41.6MB）：链接下载（默认 --group）、下载管理、系统设置、登录（区号+手机号+code）。
- MariaDB 持久化（JSON 兜底）、预置登录态 `app/presets/tdl-data`。
- 前端骨架同期完成：vue 3.5.42 + vite 8.2.2 + ts 5.9 + pinia 4 + vant 4.10；vue-tsc/eslint/build 全绿，playwright 冒烟通过。

## 调试方法备忘

- SIT 上看应用日志：`/var/log/apps/tg-download.log`
- 查进程环境变量（验证向导传参）：`cat /proc/<pid>/environ | tr '\0' '\n'`
- fnOS 桌面图标请求排查：grep nginx access.log（命令见 fnos-fpk-pitfalls.md#6）
- 复杂页面验证用 playwright-core 一次性脚本，不用 playwright-cli 守护模式。
