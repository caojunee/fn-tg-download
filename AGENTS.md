# AGENTS.md — AI Agent 开发指南

> 本文件供 AI agent（Claude Code / Cursor / Codex 等）在本项目工作前阅读。先读 `README.md` 了解项目概况，再读本文了解约定与雷区，历史经验在 `doc/memory/`。

## 项目一句话

封装 tdl 的 Telegram 下载器，Vue3 前端 + Go 后端 + MariaDB，最终交付物是**飞牛 fnOS 的 fpk 安装包**（不是网页部署）。

## 环境与凭据

| 项 | 值 |
|---|---|
| SIT 服务器 | `192.168.1.4`，ssh: `ssh -i "I:/id_rsa/oracleKey" root@192.168.1.4` |
| SIT 访问地址 | `http://192.168.1.4:5666`（fnOS 桌面），应用实际端口以安装向导为准（SIT 当前 8083） |
| MariaDB | `192.168.1.2:3306`，root / gkc2020..，SIT 用 `test` 库，生产用 `td_download` 库 |
| SIT 代理 | `192.168.1.2:20172`（本机开发代理 `127.0.0.1:10100`） |
| fnpack 打包工具 | `I:/my_project/fn_fpk_packages/fnpack.exe`（v1.2.3） |
| 本地 Telegram 登录态 | `I:/my_project/tg-download/.tdl`（**禁止入库**，也不得打进 fpk） |

## 必须遵守的约定

1. **API 响应统一 `{code, message, data}`**，前端 axios 封装在 `web/src/api/request.ts`（支持 `silent` 选项静默错误，登录态探测用）。
2. **版本号三处同步**：`fpk/tg-download/manifest` 的 `version`、`web/src/layouts/SideMenu.vue` 的 `vX.Y.Z · fpk`、发版记录 `doc/memory/changelog.md`。
3. **同文件多处修改必须串行 Edit**，并行编辑同一文件会互相覆盖（多次踩坑）。
4. **前端产物同步 fpk 前必须清空 `fpk/tg-download/app/web/assets/`**，否则新旧 hash 文件混存，包体积膨胀且可能有缓存错乱。
5. **DSN 传递必须加引号**：`cmd/main` 中 `-db-dsn "${DB_DSN}"`，DSN 里 `tcp(...)` 的括号会炸 shell。
6. **桌面入口 `app/ui/config` 里的端口/标题硬编码**，fnOS 不替换 `{display_name}/{port}` 占位符；改端口后由 `cmd/main` 启动时同步进 PostgreSQL（见下）。
7. 用户偏好：中文交流；结论先行；不要反复确认已定事项。

## 常用命令

```bash
# 前端构建
cd web && npm run build

# 后端交叉编译（交付 Linux）
cd server && GOOS=linux GOARCH=amd64 go build -o ../fpk/tg-download/app/server .

# 前端产物同步 fpk
rm -rf ../fpk/tg-download/app/web/assets ../fpk/tg-download/app/web/index.html
cp -r dist/* ../fpk/tg-download/app/web/

# 打包 fpk
cd ../fpk && I:/my_project/fn_fpk_packages/fnpack.exe build -d tg-download
```

## 浏览器验收（SIT）

- `playwright-cli` 守护进程模式不稳定，**建议用 playwright-core 写一次性脚本**：
  `NODE_PATH=C:/Users/caoju/.workbuddy/binaries/node/workspace/node_modules node script.mjs`
- 桌面端与移动端各验一遍（`useIsMobile` 断点 768px）。
- 涉及 fnOS 桌面行为的验证，优先看 SIT 上 `/usr/trim/nginx/logs/access.log` 的请求记录，比前端猜测可靠。

## fpk / fnOS 高频雷区（完整版见 doc/memory/fnos-fpk-pitfalls.md）

1. **向导字段环境变量按原名传**（`app_port`、`db_host`…），**没有** `wizard_` 前缀。
2. **fnOS 桌面入口注册晚于 install_callback 约 30 秒**，端口同步逻辑要放 `cmd/main` 的 start 流程（写 PostgreSQL `appcenter` 库 `app_service` 表，重试 5×2s）。
3. **桌面图标链路**：`/app-center-static/serviceicon/<pkg>/<manifest icon 模板>?size=256` → 实时读 `/vol1/@appcenter/<pkg>/ui/images/icon_{size}.png`，无服务端缓存；重装后不更新是**浏览器缓存**，强刷解决。
4. **manifest 的 service_port 只是默认值**，实际端口以向导 `app_port` 为准。
5. 应用进程 `run-as=root`（需要写任意下载目录）。

## 交付前检查清单

- [ ] `vue-tsc` 0 错误、eslint 0 问题、`npm run build` 通过
- [ ] 后端 `go build` 通过（Linux 交叉编译）
- [ ] fpk 内 `app/web/assets` 只有最新一套 hash 文件
- [ ] manifest / SideMenu 版本号一致，changelog 已记录
- [ ] SIT 端到端：安装向导 → 桌面打开（iframe 内嵌）→ 登录态有效 → 下载任务跑通 → 移动端可用
