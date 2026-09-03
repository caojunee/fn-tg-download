# 架构决策记录

## 1. 技术选型

- **tdl 子进程封装**而非引入 gotd 直连：tdl（v0.20.4）已解决登录态、限速、断点等难题，Go 后端只做"拼参数 + 解析输出"。`server/internal/tdl/` 按功能拆：login（登录/验证码）、probe（相册/组合消息探测）、download（下载子进程）、progress（进度解析）、proc_windows/proc_unix（进程组管理，Windows 用 Job Object 思路，Unix 用 process group kill）。
- **默认 `--group`**：`t.me/siwass/6729` 这类相册/组合消息链接单条会漏图，默认开组下载。
- **MariaDB 持久化 + Persister 接口**：`server/internal/task/persist.go` 定义接口，`store/mysql.go` 是 MariaDB 实现，另有 JSON 兜底实现——DB 不可用时应用仍可跑（本地开发不依赖 DB）。
- **统一响应 `{code, message, data}`**：前端 axios 拦截器统一处理错误 toast；`silent` 选项用于登录态探测等不想弹错的请求。
- **前端 mock 开关 `VITE_USE_MOCK`**：dev 默认开（含每秒进度推进引擎），生产构建时 mock chunk 按需加载不会下发——后端未起也能做 UI 开发。

## 2. fpk 结构决策

- **run-as=root**：下载目录用户可能选 `/vol1` 下任意位置，root 免踩权限坑。
- **不声明 install_dep_apps**：MariaDB 是外置的（192.168.1.2），应用自己处理连接失败降级到 JSON。
- **向导表单化**（0.2.0+）：DB 连接拆成 host/port/db/user/password 五个字段，端口独立可配 + 启动前 `/dev/tcp` 预检（占用则写 `TRIM_TEMP_LOGFILE` 提示用户改端口重试）。
- **DSN 由 install/config_callback 拼装**写入 `$TRIM_PKGETC/env`（`DB_DSN`、`APP_PORT`），`cmd/main` 读取；单引号转义函数 `sq_escape`。
- **`ui/config` 用 iframe 类型 + 硬编码**：桌面入口内嵌打开（体验如原生应用），占位符不替换所以硬编码；实际端口由 `cmd/main` 启动后同步进 PostgreSQL `app_service` 表（注册时序问题，见 pitfalls#5）。
- **`cmd/main` stop 先杀 tdl 子进程**再退 server，避免孤儿 tdl 占着带宽。
- **stop 用 ctl_stop=true**：fnOS 停止按钮走 `cmd/main stop`。

## 3. 前端结构决策

- **移动端适配策略**：`useIsMobile`（<768px）切换布局——桌面侧栏 ↔ `van-tabbar`（新建/任务/设置/我的），操作项用 `van-action-sheet`（该 Vant 构建无 `showActionSheet` 函数式 API，用组件式）。
- **DirPicker 目录选择器**（0.2.8+）：fnOS 没有可用的第三方目录选择 API，自建：后端 `GET /api/fs/dirs?path=` 列子目录（默认根 `/vol1`），桌面端居中弹窗（540×500）、移动端底部抽屉；目录不存在自动回落根目录。
- **iframe 内隐藏品牌区**：`SideMenu` 检测 `window.self !== window.top` 自动隐藏 `sidemenu__brand`，避免与 fnOS 系统标题栏重复。
- **路由守卫时序**：`main.ts` 中先 `userStore.init()` 完成后再 `app.use(router)`，否则首次导航早于登录态拉取会被误重定向到 /login。
- **UserMenu popover 等宽**：`:deep(.van-popover__wrapper){display:block;width:100%}`。

## 4. 图标演进

- v0.1.0–v0.2.8：Pillow 程序化生成的蓝渐变方块 + send 纸飞机（对齐 login__logo 规格：渐变 `#37bcf0→#2196d9` 135°，圆角 13/44≈30%，纸飞机占 50%）。
- v0.3.0：改用用户指定 `I:/my_project/logo.png`（Go gopher 骑纸飞机，1100×1000 RGBA 透明底），Pillow 居中方形化 + 4% 边距 + LANCZOS。
