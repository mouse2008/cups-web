# Frontend

CUPS Web 前端，基于 Vue 3 + Vite + [Nuxt UI v4](https://ui.nuxt.com/) + Tailwind CSS v4，推荐使用 [Bun](https://bun.sh/) 管理依赖。

构建产物（`dist/`）会被根目录的 `frontend/embed.go` 通过 `go:embed` 打包进 Go 二进制，因此 **发布前必须先构建前端**。

## 开发

```bash
cd frontend
bun install
bun run dev          # Vite 开发服务器（默认 :5173，/api 代理到 :8090）
```

配合后端本地调试：

```bash
# 另开终端，在仓库根目录启动后端在 :8090
LISTEN_ADDR=:8090 go run ./cmd/server
```

## 构建

```bash
bun run build        # 产物输出到 frontend/dist
```

也可以直接在仓库根目录执行 `make frontend`。

## 目录结构

```text
src/
├── main.js              # Vue app 入口
├── App.vue              # 顶层布局 + 鉴权跳转
├── router/index.js      # hash 路由 + session 守卫
├── views/               # LoginView / PrintView / AdminView
├── components/          # 业务组件
├── utils/               # api / file / format / printerAcl
└── index.css            # 全局样式
```

## 与打印机 ACL 相关的前端约定

- `PrintView` 只展示当前登录用户可见的打印机；后端 `/api/printers` 已做同样过滤，前端不是唯一防线。
- 管理后台里的“打印机权限”支持 `role / group / user` 三类主体：
  - `role`：按角色整组授权
  - `group`：按本地打印组批量授权，可把多个 LDAP / 本地用户归为一组
  - `user`：对单用户做 allow / deny 例外覆盖
- `src/utils/printerAcl.js` 负责 ACL 规则草稿、主体标签与校验辅助逻辑；改管理员权限 UI 时优先复用它，避免把规则拼装散落在 `AdminView.vue` 各处。

更详细的架构和 API 约定见仓库根目录的 [AGENTS.md](../AGENTS.md)。
