# fn-tg-download

基于 [iyear/tdl](https://github.com/iyear/tdl) 的 Telegram 下载工具，打包为**飞牛 fnOS 的 fpk 应用**。

- **前端**：Vue 3.5 + Vite 8 + TypeScript + Pinia + Vue-Router + Vant 4 + SCSS
- **后端**：Go（net/http，无框架），统一响应 `{code, message, data}`
- **核心**：封装 tdl 子进程，支持链接下载（默认 `--group` 相册/组合消息探测）、任务进度管理、登录（区号+手机号+验证码）、系统设置
- **持久化**：MariaDB（JSON 兜底）
- **目标平台**：飞牛 fnOS（fpk 安装包，x86）

## 功能

| 模块 | 说明 |
|---|---|
| 用户登录 | 区号+手机号 → 验证码（支持 2FA 密码），登录态存 tdl 数据目录 |
| 新建下载 | 多链接批量提交、相册探测开关、并发数、扩展名过滤 |
| 链接类型 | 消息 / 相册 / 组合消息 / 整频道（`t.me/<user>` 或 `t.me/c/<id>` 不带消息 ID，先导出消息列表再批量下载）；私有链接探测失败由 tdl peers 缓存兜底 |
| 目录规则 | 自动按频道名分子目录（`<下载路径>/<频道名>`）；私有频道回退用对话 ID；非法字符自动清洗 |
| 下载管理 | 进度/速率/ETA 实时刷新、进行中/历史分组、文件明细（含排队占位）、暂停续传、取消可选删分片 |
| 系统设置 | 下载路径（目录选择器）、代理、并发/限速、tdl 路径 |

移动端（<768px）自动切换为底部 tabbar 布局。

## 目录结构

```
fn-tg-download/
├── web/                  # 前端（Vue3 + Vite）
│   ├── src/
│   │   ├── views/        # Login / NewDownload / DownloadManager / SystemSettings
│   │   ├── layouts/      # MainLayout（侧栏+移动端 tabbar）、SideMenu、UserMenu
│   │   ├── components/   # DirPicker（目录选择器）、AppIcon、TaskItem、StatusTag
│   │   ├── api/          # request.ts（axios 封装）、auth/task/settings/fs.ts
│   │   └── composables/  # useIsMobile.ts 等
│   └── dist/             # 构建产物
├── server/               # Go 后端
│   ├── main.go           # 入口：flag 解析、路由注册、优雅退出
│   └── internal/
│       ├── api/          # server.go 路由 / handlers.go 业务 / fs.go 目录浏览
│       ├── config/       # flags: -addr -data-dir -tdl -tdl-data -ns -web -db-dsn
│       ├── task/         # 任务管理器 + 持久化（Persister 接口）
│       ├── store/        # MariaDB 实现
│       ├── settings/     # 设置读写
│       └── tdl/          # tdl 子进程封装：login/probe/download/progress
├── fpk/
│   ├── tg-download/      # fpk 源目录（见下）
│   └── tg-download.fpk   # 打包产物
├── bin/tdl.zip           # tdl 二进制压缩包（打包时放入 app/bin）
├── dist/                 # 手工部署用的产物（server + tdl 解压目录）
└── doc/memory/           # AI agent 开发记忆（踩坑/决策/环境）
```

### fpk 源目录（fpk/tg-download/）

```
├── manifest              # 应用元信息（version / service_port 等）
├── ICON.PNG / ICON_256.PNG
├── wizard/install        # 安装向导表单（DB 连接、端口、下载路径、代理）
├── cmd/                  # 生命周期脚本：main（启停）、install/config_callback 等
├── config/               # privilege / resource
└── app/                  # 安装后落地文件
    ├── server            # Go 后端二进制（Linux）
    ├── bin/              # tdl
    ├── web/              # 前端构建产物（index.html + assets/）
    ├── ui/config         # fnOS 桌面入口定义（iframe 类型，硬编码端口）
    ├── ui/images/        # 图标（icon-64/256.png，fnOS 按 {0} 模板取尺寸）
    └── presets/tdl-data  # 预置登录态（安装时复制到 var，存在则跳过）
```

## 本地开发

```bash
# 前端（dev mock 可在 src/api 下 VITE_USE_MOCK 控制）
cd web
npm install
npm run dev        # 默认 5173，proxy /api → 127.0.0.1:8080

# 后端（需要本机有 tdl 可执行文件）
cd server
go build -o server-local.exe .
./server-local.exe -addr 127.0.0.1:8080 -data-dir ./local-data -tdl <tdl路径> -tdl-data <tdl数据目录>
# 带 DB：-db-dsn "user:pass@tcp(host:3306)/db?charset=utf8mb4&loc=Local"
```

登录态本地开发可复用 `fpk/tg-download/app/presets/tdl-data` 目录（即 tdl 数据目录）。

## 构建 fpk

```bash
# 1. 构建前端
cd web && npm run build

# 2. 同步产物到 fpk（注意先清空 assets 防止旧 hash 文件残留）
rm -rf fpk/tg-download/app/web/assets fpk/tg-download/app/web/index.html
cp -r web/dist/* fpk/tg-download/app/web/

# 3. 打包（fnpack v1.2.3，位于 I:/my_project/fn_fpk_packages/）
cd fpk
I:/my_project/fn_fpk_packages/fnpack.exe build -d tg-download
# 产物：fpk/tg-download.fpk
```

后端二进制 `fpk/tg-download/app/server` 为 Linux amd64 版本，改动 Go 代码后需交叉编译：

```bash
cd server && GOOS=linux GOARCH=amd64 go build -o ../fpk/tg-download/app/server .
```

## 部署

1. fnOS 应用中心 → 手动安装 → 选择 `fpk/tg-download.fpk`
2. 安装向导填写：数据库连接（host/port/库名/用户/密码）、应用端口（默认 8080，被占用则改）、下载路径、代理
3. SIT 环境：`192.168.1.4`（库 `test`）；生产环境：库名改 `td_download`

> 版本升级后桌面图标若未刷新，属浏览器缓存，Ctrl+F5 强制刷新即可（详见 doc/memory/fnos-fpk-pitfalls.md）。

## 文档索引

- [AGENTS.md](./AGENTS.md) —— AI agent 开发必读（约定/命令/验收流程）
- [doc/memory/](./doc/memory/) —— 历史踩坑、架构决策、环境信息、版本记录
