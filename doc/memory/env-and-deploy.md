# 环境与部署信息

## 本机（开发机，Windows）

- 项目位置：`I:/my_project/fn-tg-download`（2026-09-04 从 WorkBuddy workspace 迁入）
- 本地代理：`127.0.0.1:10100`
- 本地 Telegram 登录态：`I:/my_project/tg-download/.tdl`（**不得**预置进 fpk，也禁止入库）
- Node：managed `C:/Users/caoju/.workbuddy/binaries/node/versions/22.22.2-2/node.exe`（全局包装到 `.../node/workspace`）
- Python（含 Pillow venv）：`C:/Users/caoju/.workbuddy/binaries/python/envs/pillow/Scripts/python.exe`
- playwright-core 脚本运行方式：`NODE_PATH=C:/Users/caoju/.workbuddy/binaries/node/workspace/node_modules node script.mjs`

## SIT（192.168.1.4，飞牛 fnOS）

- ssh：`ssh -i "I:/id_rsa/oracleKey" root@192.168.1.4`
- fnOS 桌面：`http://192.168.1.4:5666`
- 应用实际端口：8083（安装向导 app_port 填的值）
- 数据库：MariaDB `192.168.1.2:3306`，root / `gkc2020..`，SIT 用 **test** 库
- SIT 代理：`192.168.1.2:20172`
- 关键路径：
  - 应用安装目录：`/vol1/@appcenter/tg-download/`（= `/var/apps/tg-download/target`）
  - 应用配置：`/vol1/@appconf/tg-download/env`（DB_DSN、APP_PORT）
  - 应用数据：`/vol1/@appdata/tg-download/`（tdl-data、下载）
  - 日志：`/var/log/apps/tg-download.log`
  - fnOS 桌面前端：`/usr/trim/www/assets/index-*.js`；nginx 日志：`/usr/trim/nginx/logs/access.log`
  - appcenter 数据库：PostgreSQL `appcenter` 库（`app_service` 表存桌面入口 URL；`su postgres -c 'psql -d appcenter ...'`）

## 生产（规划）

- 同 SIT 流程，安装向导库名改 **td_download**，库名之外按需调整。

## 登录态说明

- **包内不预置登录态**（0.4.6 起移除 `app/presets/tdl-data`）：安装包会带上开发者真实 session，属于账号泄露风险；首次使用一律在应用内登录。
- 登录态落地 `/vol1/@apphome/tg-download/.tdl/data/default`，升级/重装会保留（fnOS 不清 `@apphome`/`@appdata`），所以重装后仍是已登录状态——这是预期行为。
- 若要换账号：删掉该 `default` 文件后重启应用，或走应用内登录页重新登录。
