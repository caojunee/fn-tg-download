# fnOS fpk 踩坑记录（血泪版）

> 按"现象 → 根因 → 解法"整理，全部为 2026-09 实测结论，勿凭直觉推翻。

## 1. 安装向导环境变量

- **现象**：回调脚本按 `wizard_app_port` 读环境变量全为空，应用打不开。
- **根因**：fnOS 把向导字段**按原名**传环境变量（`app_port`、`db_host`、`db_port`、`db_name`、`db_user`、`db_password`、`download_path`、`proxy`），不带 `wizard_` 前缀。
- **验证手段**：`cat /proc/<pid>/environ`。
- 系统自带变量：`TRIM_PKGVAR`（=/vol1/@appdata/<pkg>）、`TRIM_PKGETC`、`TRIM_APPDEST`、`TRIM_SERVICE_PORT`、`TRIM_APPNAME`、`TRIM_TEMP_LOGFILE`。

## 2. DSN 括号炸 shell

- **现象**：带 DB 启动失败。
- **根因**：`-db-dsn ${DB_DSN}` 未加引号，DSN 中 `tcp(192.168.1.2:3306)` 的括号被 shell 解析。
- **解法**：`-db-dsn "${DB_DSN}"`。

## 3. 桌面入口占位符不替换

- **现象**：桌面入口显示 `{display_name}`、`{port}` 原文。
- **根因**：fnOS 不替换 `app/ui/config` 里的占位符。
- **解法**：全部硬编码真实值（title、port、url）。port 字段只是初始值，实际以向导端口为准，靠第 5 条同步。

## 4. 桌面打开跳新标签页

- **解法**：`app/ui/config` 中 `"type": "iframe"`（而非 `"url"`），即可在 fnOS 窗口内嵌打开。

## 5. 安装端口与实际访问端口不一致（最隐蔽）

- **现象**：向导填 8062/8065，桌面入口仍指向 8080。
- **根因**：fnOS 安装时把桌面入口 URL 写进 **PostgreSQL `appcenter` 库 `app_service` 表**（`service_name='tg-download.main'`），该注册发生在 install_callback **之后约 30 秒**，且回调阶段无法覆盖。
- **解法**：端口同步挪到 `cmd/main` start 流程——连 PostgreSQL（重试 5 次 × 2s）执行 UPDATE，把 `app_service.url` 的端口改成实际 `APP_PORT`。连接方式：`psql -h 127.0.0.1 -U postgres -d appcenter`（su postgres 免密）。

## 6. 桌面图标不更新（2026-09-04 定案）

完整链路（实测）：
1. 桌面 `<img class="app-icon">` 的 src = `/app-center-static/serviceicon/<pkg>/ui/images/icon_{0}.png?size=256`（`{0}` 模板来自 `app/ui/config` 的 icon 字段，fnOS 按 size 替换）。
2. trim 后端（`/usr/trim/bin/trim_app_center`）**实时读磁盘** `/vol1/@appcenter/<pkg>/ui/images/icon_{size}.png`，**无服务端缓存**（响应字节数随磁盘文件变化证实）。
3. 安装向导预览图走 `/app-center-static/tmpicon/<pkg>/<version>/1/icon.png`（读包内 ICON.PNG）。
4. 图标加载失败回退 `/assets/app-icon-default-CvM1COkP.png`（前端 JS：`/usr/trim/www/assets/index-*.js`）。

**结论**：重装后桌面图标没变 = 浏览器缓存了旧响应（nginx 日志可证实重装后浏览器未再发请求）。**Ctrl+F5 强刷即解决**，服务端无需任何处理。日志排查命令：

```bash
grep -o 'GET /app-center-static/[^ ]*<pkg>[^ ]* HTTP/1.1" [0-9]* [0-9]*' /usr/trim/nginx/logs/access.log | sort | uniq -c
```

## 7. 图标文件规格

- 需要 6 处同步：fpk 根 `ICON.PNG`(64)/`ICON_256.PNG` + `app/ui/images/{icon-64,icon_64,icon-256,icon_256}.png`（横线/下划线两套命名都放，内容相同）。
- 安装时 fnOS 把根目录 ICON 拷到 `/var/apps/<pkg>/`。
- 当前图标：`I:/my_project/logo.png`（gopher 骑纸飞机，1100×1000 RGBA）→ Pillow 居中到正方形、留 4% 边距、LANCZOS 缩放，透明背景。

## 8. 打包/构建杂项

- `fnpack build -d tg-download`（v1.2.3），产物在 fpk/ 下。
- vite 构建曾报 `@types/node` 缺失 → `npm i -D @types/node`；`scss api:'modern-compiler'` 已移除。
- 前端产物同步 fpk 前必须清空 `app/web/assets/`（新旧 hash 混存）。
- 本地 `server-local.exe`/vite dev server 会锁文件，导致项目目录删不掉/迁移失败 → 先 `tasklist /m rolldown-binding...`、`tasklist /fo csv | grep server-local` 找到占用进程 kill，再 rimraf。
- Windows 删大目录（node_modules）：回收站方式（genie-trash）易失败，用 `rimraf`（bin: `node_modules/rimraf/dist/esm/bin.mjs`）。

## 9. tdl 数据目录布局（登录态"未登录"根因）

- 官方 tdl 布局：`<数据目录>/data/<namespace>` 为 bolt 文件，bucket=namespace，key=`session`。默认数据目录 `~/.tdl`。
- 本项目 server 的 `OpenBolt(dir, ns)` 直接打开 `<dir>/<ns>`，因此 **`-tdl-data` 必须传到 `.../tdl-data/data` 这一层**（不是 tdl-data 根）。
- 传错层级时 server 会在根下自建一个空的 `<ns>` 文件，HasSession 永远 false → 界面一直"未登录"，预置登录态完全失效。
- 判断依据：`ls -la` 对比——登录成功的 `default` 文件约 128–131KB，自建空库只有 32KB 且 mtime 是请求时刻。
- 设置页"认证信息保存在 下载路径/.tdl"曾是前端写死的误导文案（0.3.1 已改为后端返回真实 tdlDataDir）。
- `Me()`（gotd client.Auth().Status）需要连 Telegram，走 settings 里配置的代理；HasSession 只读本地 bolt 不需要网络。

## 10. tdl 登录态目录定版：TRIM_PKGHOME（0.3.2 起）

- fnOS 应用没有 HOME 环境变量；fnOS 注入 **TRIM_PKGHOME=/vol1/@apphome/<pkg>**（trim_app_center 注入表实测确认，二进制内 `TRIM_APPDESTTRIM_PKGHOMETRIM_PKGMETA` 连串）。
- 登录态定版路径：`/vol1/@apphome/tg-download/.tdl/data/default`（官方 tdl 布局 `<数据目录>/data/<ns>`）。
- cmd/main 写法：`TDL_DATA_DIR="${TRIM_PKGHOME:-/vol1/@apphome/${TRIM_APPNAME:-tg-download}}/.tdl/data"`——不依赖 HOME，fnOS 启动/SSH 手动启动结果一致。
- start 时自动迁移：目标无 default 时，从旧位置 `${TRIM_PKGVAR}/tdl-data/data/default` 迁移（0.3.1 及以前）；**不再从包内 presets 注入登录态**（会带上开发者账号）。
- 排查手段：`tr '\0' '\n' < /proc/<pid>/environ` 看真实环境；SSH 手动重启的进程环境不可作准（会继承 SSH shell 的 HOME=/root）。
- `/root/.tdl` 曾短暂使用后废弃；注意 tdl CLI 默认 storage path=/root/.tdl/data，若 SSH 手动跑 tdl 命令需显式 `-d /vol1/@apphome/tg-download/.tdl`。

## 11. tdl tracker 行真实格式(0.3.4 定版,SIT 实测抓取)

- **tdl 进度 tracker 行不含文件名**!真实格式(SIT 自编译 0.20.x,http 代理):
  `Telegram News(1005640892):193~  ...  50.5% [####....] [1.00 MB in 7s; 139.40 KB/s]`
  显示名为 `{聊天标题}({对话ID}):{消息ID}~`,完成后百分比变为 `done!`。
- 帧结构:CPU/Memory 统计行(无标记)→ 每个 tracker 行 → 进度条行(`[####....]`,无标记)。
 此前按"行首 token 是文件名"解析完全错误(剥 ANSI 后首 token 是标题首词,如 `Telegram`),
 被 `^\d+_\d+_` 过滤后 filePerc 永远为空 → 文件追踪与删除分片双双失效(0.3.3 的"验证成功"
 实为磁盘扫描兜底的假象)。
- 0.3.4 方案:ParseProgressLine 用 `\((\d+)\):(\d+)~` 提取对话/消息 ID(百分比只在标记之后找,
 避免聊天标题里的数字误匹配;`done!` 记 100%);真实文件名由 watchTask 每秒按
 `{对话ID}_{消息ID}_*` 前缀 glob 下载目录解析(td l 默认模板 `{对话ID}_{消息ID}_{文件名}`,
 下载中带 `.tmp` 后缀,完成时 tdl 自动改名)。
- 删除分片同样按 `{对话ID}_{消息ID}_*.tmp` glob 前缀匹配——文件名含空格/emoji/括号都不影响,
 且不会误删同目录其他任务的分片。
- 诊断手段:`timeout 40 tdl -n default --storage 'type=bolt,path=...' --proxy http://... dl ... > log 2>&1`
 后 `cat -v` 看 ANSI 原始帧。注意:代理配置若是 `http://` 用 socks5 测会"挂起 0 输出"
 (端口通但握手超时),先 `curl --proxy <同协议>` 验证连通性。
- 纯文本消息(无媒体)tdl exit 0 空跑,任务会显示 completed 但 0 文件——边界情况,后续可在
 probe 阶段拦截。
