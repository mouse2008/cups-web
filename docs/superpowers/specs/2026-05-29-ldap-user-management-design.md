# LDAP 用户管理设计

## 概述

为 `cups-web` 增加基于 LDAP 的用户目录能力，同时保留现有本地用户模型、会话流程和权限校验机制。

已确认的目标行为如下：

- 支持将 LDAP 用户同步到本地数据库。
- 支持 LDAP 用户通过 LDAP 凭据登录。
- 即使是 LDAP 用户，角色仍由本地管理。
- 支持管理员手动触发同步，也支持按计划定时同步。
- 本地用户和受保护的默认 `admin` 账号继续按现有方式工作。

这是一个“混合身份模型”：认证来源可以是本地数据库，也可以是 LDAP；但应用内部仍以本地 `users` 表作为会话身份、权限判断、打印记录归属和管理后台用户列表的统一数据源。

## 目标

- 支持从 LDAP 导入和刷新用户。
- 支持 LDAP 用户通过 LDAP 密码认证登录。
- 保留 LDAP 用户的本地角色分配能力。
- 复用现有 `users` 表和 session 模型，避免影响打印记录和管理后台功能。
- 支持定时后台同步，以及管理员手动触发同步。
- 在 LDAP 被关闭或暂时不可用时，本地账号仍能正常工作。

## 非目标

- 第一版不做 LDAP 组到角色的映射。
- 第一版不支持在应用内为 LDAP 用户修改密码或重置密码。
- 第一版不支持从应用中删除 LDAP 目录里的真实用户。
- 第一版不引入异步任务队列或同步进度任务系统。
- 第一版不在 LDAP 用户从目录中消失时自动硬删除本地用户。

## 当前实现现状

当前系统只有一张 `users` 表，使用 bcrypt 存储密码哈希，角色保存在本地，登录后用 `securecookie` 发放 session cookie。

与当前认证和用户管理相关的核心实现：

- `cmd/server/auth_handlers.go`
  直接从本地 `users` 表认证用户名和密码。
- `internal/store/users.go`
  假设所有用户都是本地行，并且都依赖 `password_hash`。
- `cmd/server/admin_handlers.go`
  管理员可以创建、更新、删除本地用户。
- `cmd/server/bootstrap.go`
  保证受保护的本地 `admin` 账号始终存在。
- `cmd/server/maintenance.go`
  已经具备定时后台维护循环，适合挂载 LDAP 定时同步。

## 选定架构

采用“单表扩展 + 混合认证来源”的方案。

本地数据库继续作为以下能力的权威来源：

- session 身份
- 打印记录里引用的用户 ID
- 角色
- 管理后台用户列表
- 导入后的联系人信息

LDAP 作为以下能力的权威来源：

- LDAP 用户的密码校验
- 用于发现和刷新 LDAP 用户的目录身份属性

这样做的好处是：不需要重写现有 session、打印记录归属和基于 `users.id` 的查询关系，同时又能支持 LDAP 登录与目录同步。

## 数据模型变更

在现有 `users` 表中新增以下字段：

- `auth_source TEXT NOT NULL DEFAULT 'local'`
- `ldap_dn TEXT`
- `ldap_uid TEXT`
- `ldap_sync_enabled INTEGER NOT NULL DEFAULT 0`
- `ldap_present INTEGER NOT NULL DEFAULT 1`
- `last_ldap_sync_at TEXT`
- `last_login_at TEXT`

### 字段语义

- `auth_source`
  - `local`：密码使用本地 `password_hash`
  - `ldap`：密码通过 LDAP 校验
- `ldap_dn`
  - LDAP 用户的 DN，用于 bind 认证和稳定匹配
- `ldap_uid`
  - 应用侧保存的 LDAP 唯一标识，来源于配置的登录属性
- `ldap_sync_enabled`
  - 标记该用户是否由 LDAP 同步管理
- `ldap_present`
  - 标记该用户是否在最近一次同步中仍然存在于 LDAP 目录中
- `last_ldap_sync_at`
  - 该用户最近一次被成功同步的时间
- `last_login_at`
  - 该用户最近一次成功登录时间

### 现有字段保留策略

- `password_hash`
  为兼容现有 schema 继续保留，但 LDAP 用户登录时不使用该字段认证。
- `role`
  仍然是权限判断的唯一依据，LDAP 同步不会覆盖它。
- `contact_name`、`phone`、`email`
  可以从 LDAP 同步补齐，同时允许管理员在本地维护。

## LDAP 配置模型

LDAP 配置继续存放在现有 `settings` 表中，不额外新增配置文件。

新增配置项：

- `ldap_enabled`
- `ldap_url`
- `ldap_bind_dn`
- `ldap_bind_password`
- `ldap_base_dn`
- `ldap_user_filter`
- `ldap_login_attr`
- `ldap_display_name_attr`
- `ldap_email_attr`
- `ldap_phone_attr`
- `ldap_sync_interval_minutes`

### 配置行为约束

- `ldap_enabled = false` 时，所有 LDAP 登录和同步逻辑都跳过。
- LDAP 配置读取失败或 LDAP 服务连接失败，不得影响本地用户登录。
- `ldap_bind_password` 和当前的 session 密钥一样，第一版继续存储在 `settings` 表中，但必须视为敏感信息，不能写入日志，也不能在接口响应中明文回显。

## 认证流程

### 本地用户登录

如果查到的本地用户行 `auth_source = 'local'`，则继续走当前 bcrypt 密码校验流程。

### LDAP 用户登录

如果 LDAP 已启用，登录流程按以下顺序执行：

1. 读取提交的用户名和密码。
2. 先按用户名查询本地用户。
3. 如果本地用户存在且 `auth_source = 'local'`，只走本地认证，不再尝试 LDAP。
4. 否则使用 `ldap_login_attr` 和 `ldap_user_filter` 到 LDAP 中搜索该用户名。
5. 要求 LDAP 搜索结果必须唯一命中一条记录。
6. 使用命中的用户 DN 和用户输入的密码执行 LDAP bind。
7. bind 成功后：
   - 按 `ldap_uid` 查找已有本地用户，必要时回退到 `ldap_dn`
   - 如果不存在本地映射，则自动创建一条本地用户：
     - `auth_source = 'ldap'`
     - 默认 `role = 'user'`
     - 写入同步得到的联系人信息
     - 写入同步标记字段
   - 如果本地映射已存在，则刷新 LDAP 管理字段
   - 更新 `last_login_at`
   - 继续使用现有 session 机制发放 cookie，session 中仍保存本地用户 ID 和本地角色
8. 认证失败时，返回通用认证错误，不向前端泄露 LDAP 目录细节。

### 冲突处理规则

- 如果本地已经存在同名 `local` 用户，则 LDAP 自动建档不能覆盖它。
- 如果 LDAP 搜索命中多条记录，则认证失败。
- 如果 LDAP 查询因网络或服务不可用失败，则本地用户仍可登录，LDAP 登录返回通用失败。

## 同步策略

同步分三种触发方式：

### 1. 登录时轻量刷新

LDAP 用户登录成功后，仅刷新当前这个用户的目录资料和同步标记，不触发全量扫描。

这样可以避免每次登录都把一次交互请求变成整库同步。

### 2. 管理员手动同步

新增管理员专用接口，允许手动执行一次全量 LDAP 同步。

第一版建议同步执行，直接复用当前项目的请求处理模式，不引入任务队列和后台作业框架。

### 3. 定时后台同步

在现有 `maintenance.go` 后台维护循环中扩展 LDAP 定时同步逻辑。

行为要求：

- LDAP 关闭时不运行
- 同步间隔为 0 或非法值时不运行
- 同步失败只记录日志，不影响后续下一轮同步
- LDAP 错误不能导致进程崩溃

## 同步语义

全量同步应执行以下步骤：

1. 使用配置的 `base DN` 和用户过滤器查询 LDAP。
2. 将每条目录记录转换成应用内部统一的标准结构。
3. 优先按 `ldap_uid` 匹配已有本地用户，必要时回退到 `ldap_dn`。
4. 对不存在的 LDAP 用户，新建本地用户行，并写入：
   - `auth_source = 'ldap'`
   - `ldap_sync_enabled = 1`
   - 默认 `role = 'user'`
5. 对已存在的 LDAP 用户，更新其目录字段和同步标记。
6. 对以下字段保留本地策略，不允许 LDAP 覆盖：
   - `role`
   - 本地用户的密码数据
   - 本地用户的认证来源
7. 对上一轮已同步、但本轮 LDAP 未再出现的用户，标记 `ldap_present = 0`。

### 联系方式字段合并规则

对于 `contact_name`、`phone`、`email`，第一版采用“本地为空时由 LDAP 补齐，本地非空时不覆盖”的规则。

原因：

- 你已经确认 LDAP 用户角色和用户管理仍要保留本地控制
- 强覆盖会把管理员手工修正的信息刷掉
- 该规则简单、稳定、可预测

## 管理后台与 API 变更

### 用户列表

管理员用户列表接口需要新增以下字段：

- `authSource`
- `ldapSyncEnabled`
- `ldapPresent`
- `lastLdapSyncAt`
- `lastLoginAt`

### 新建本地用户

`POST /api/admin/users` 保持现有行为，只允许创建本地用户。

### 编辑用户

对于 LDAP 用户：

- 用户名只读
- 密码输入框隐藏或禁用
- 角色仍可编辑
- 联系人、电话、邮箱仍可编辑

对于本地用户：

- 保持当前行为不变

### 删除用户

第一版不支持删除 LDAP 用户。

原因：

- 当前 schema 中 `print_jobs.user_id -> users.id` 使用 `ON DELETE CASCADE`
- 如果直接删除 LDAP 用户行，会连带删除其历史打印记录
- 在没有完整软删除模型之前，禁止删除比错误地丢历史数据更安全

第一版必须满足的行为：

- 本地用户继续支持现有删除逻辑
- LDAP 用户在管理后台中不可删除
- 如果某 LDAP 用户已经从目录中消失，则同步时将其标记为 `ldap_present = 0`
- `ldap_present = 0` 的用户后续不能再通过 LDAP 登录
- 该用户已有的历史打印记录继续保留并可查询

### 设置页面

在后台“系统设置”中新增 LDAP 配置区，建议包含：

- LDAP 开关
- 服务地址
- bind DN
- bind 密码
- base DN
- 用户过滤器
- 登录属性
- 显示名属性
- 邮箱属性
- 电话属性
- 同步间隔
- 手动同步按钮

敏感字段要求：

- 已保存的 `ldap_bind_password` 不能以明文回显
- 设置读取接口应返回“是否已设置密码”的状态，或返回掩码占位，不返回真实值

## API 面设计

### 需要扩展的现有接口

- `GET /api/admin/users`
- `PUT /api/admin/users/{id}`
- `GET /api/admin/settings`
- `PUT /api/admin/settings`
- `POST /api/login`

### 新增接口

- `POST /api/admin/ldap/sync`
  - 管理员手动触发一次 LDAP 全量同步

## 同步状态元数据

为了让管理员看到最近同步情况，在 `settings` 中增加以下状态字段：

- `ldap_last_sync_started_at`
- `ldap_last_sync_finished_at`
- `ldap_last_sync_status`
- `ldap_last_sync_message`
- `ldap_last_sync_count`

第一版不新增单独状态表，也不新增单独的同步状态接口；这些状态直接通过 `GET /api/admin/settings` 返回即可，足够支撑后台展示和排障。

## 错误处理

### 认证错误

- 对前端统一返回通用认证失败信息。
- 服务端日志保留可排查信息，但不得打印密码或 bind 密钥等敏感内容。

### 配置错误

- 管理员保存 LDAP 配置时，接口层应尽量提前做参数校验。
- 如果 LDAP 必填配置缺失，则应阻止 LDAP 登录和 LDAP 同步，但不能影响本地账号登录。

### 目录结果异常

- 查询结果为 0 条：认证失败
- 查询结果多于 1 条：认证失败
- 同步过程中缺少关键 LDAP 属性：跳过该用户并记日志

### LDAP 服务可用性

- LDAP 不可用不能阻止应用启动。
- 定时同步失败只记日志，并在下一轮按计划继续重试。

## 安全要求

- 即使启用了 LDAP，也必须保留受保护的本地 `admin` 账号作为兜底入口。
- LDAP 同步绝不能修改或删除受保护的本地 `admin`。
- `ldap_bind_password` 不能出现在日志和接口明文响应中。
- 继续复用现有 session 与 CSRF 保护，不改变其安全边界。
- 实现阶段优先支持 LDAPS 或可升级到 TLS 的 LDAP 连接方式。

## 迁移步骤

1. 为 `users` 表新增 LDAP 相关字段，并保持迁移幂等。
2. 增加 LDAP 配置读写和校验逻辑。
3. 实现 LDAP 客户端或 LDAP 服务层。
4. 将登录逻辑改为本地认证 + LDAP 认证并存。
5. 扩展后台用户接口和设置接口。
6. 增加管理员手动同步接口。
7. 将定时同步接入现有维护循环。
8. 更新前端后台页面和必要的登录提示。
9. 为迁移、登录、同步、编辑限制等行为补测试。

## 测试策略

至少覆盖以下行为：

- 老数据库升级后，新增 LDAP 字段迁移成功
- 本地用户登录行为不变
- LDAP 用户首次登录自动建档
- LDAP 用户再次登录时会刷新 LDAP 字段，但不会覆盖本地角色
- LDAP 用户不能通过后台修改密码
- 管理员手动同步可以新增和更新 LDAP 用户
- 全量同步会把目录中已消失的 LDAP 用户标记为 `ldap_present = 0`
- LDAP 关闭时定时同步不会运行
- LDAP 出错时不会影响本地认证，也不会让后台维护 goroutine panic

测试实现建议：

- 提供一个可替换的 LDAP 抽象层，便于单元测试 mock
- 不要把 handler 测试直接绑定到真实 LDAP 服务

## 推荐实现边界

建议新增一个专门的 LDAP 服务层，位置可以是 `internal/auth` 下的 LDAP 子模块，或新增 `internal/ldap` 包。handler 保持尽量薄，只负责流程编排。

职责边界建议如下：

- handler 决定走本地认证还是 LDAP 认证
- LDAP 服务层负责搜索、bind、属性标准化和同步
- store 层负责 schema 迁移和用户持久化

这样可以把“混合认证 + 目录同步”的复杂度集中在可测试的边界里，避免把 LDAP 逻辑散落到 handler、store 和后台维护循环各处。
