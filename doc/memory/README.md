# doc/memory — AI agent 记忆库

本目录沉淀本项目开发过程中的全部经验记忆，**接手开发前必读**。

| 文件 | 内容 |
|---|---|
| [fnos-fpk-pitfalls.md](./fnos-fpk-pitfalls.md) | fnOS fpk 全部踩坑：向导环境变量、DSN 转义、桌面入口/端口/图标链路、打包杂项。**最高价值**。 |
| [architecture-decisions.md](./architecture-decisions.md) | 架构决策及理由：tdl 子进程封装、Persister 接口、mock 开关、DirPicker、移动端策略等。 |
| [env-and-deploy.md](./env-and-deploy.md) | 环境/凭据/路径：SIT ssh、MariaDB、代理、预置登录态、SIT 关键路径清单。 |
| [changelog.md](./changelog.md) | 版本历史（0.1.0 → 0.3.0），每版改了什么、为什么，含调试方法备忘。 |

维护约定：

- 每次发版在 `changelog.md` 顶部追加版本段（版本号同步 manifest 与 SideMenu）。
- 新踩的坑追加进 `fnos-fpk-pitfalls.md`（现象 → 根因 → 解法格式）。
- 改变架构/选型时在 `architecture-decisions.md` 记录决策与理由。
- 环境变更（IP、凭据、路径）及时更新 `env-and-deploy.md`。
