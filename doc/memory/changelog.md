# 版本记录

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
