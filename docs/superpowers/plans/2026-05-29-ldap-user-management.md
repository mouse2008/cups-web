# LDAP 用户管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `cups-web` 增加 LDAP 用户同步、LDAP 登录、本地角色管理、管理员手动同步和定时后台同步能力，同时保持本地用户与默认 `admin` 逻辑不回归。

**Architecture:** 继续以本地 `users` 表作为统一身份主表，新增 LDAP 来源字段和同步状态字段；认证层改为“本地认证 + LDAP 认证并存”；LDAP 目录同步由独立服务层负责，管理员接口和后台定时任务只做流程编排，不直接实现目录逻辑。

**Tech Stack:** Go 1.26、SQLite、Gorilla Mux、Vue 3、Nuxt UI、`github.com/go-ldap/ldap/v3`

---

## 文件结构

### 后端核心改动

- 修改: `internal/store/store.go`
  - 为 `users` 表增加 LDAP 字段和 settings 常量。
- 修改: `internal/store/users.go`
  - 扩展 `User` 结构和 LDAP 用户相关查询、创建、更新接口。
- 新建: `internal/store/users_ldap_test.go`
  - 覆盖 schema 迁移、LDAP 用户 upsert、失联标记行为。
- 修改: `internal/store/settings.go`
  - 增加 bool/string settings 辅助函数，支持 LDAP 配置读写。
- 新建: `internal/ldap/service.go`
  - 封装 LDAP 搜索、bind、属性标准化、单用户刷新和全量同步。
- 新建: `internal/ldap/config.go`
  - 定义 LDAP 配置结构、加载逻辑和校验逻辑。
- 新建: `internal/ldap/client.go`
  - 抽象底层 LDAP 客户端接口，便于测试替换。
- 新建: `internal/ldap/service_test.go`
  - 用 fake client 测 LDAP 认证、同步、冲突和缺失用户标记。
- 修改: `cmd/server/app.go`
  - 新增应用级 LDAP 服务引用或访问入口。
- 修改: `cmd/server/auth_handlers.go`
  - 登录改为混合认证；更新 `last_login_at`。
- 新建: `cmd/server/auth_handlers_test.go`
  - 覆盖本地登录、LDAP 首登建档、LDAP 再登刷新、本地用户优先级。
- 修改: `cmd/server/admin_handlers.go`
  - 用户响应扩展、LDAP 设置读写、LDAP 用户编辑限制、手动同步接口。
- 新建: `cmd/server/admin_handlers_test.go`
  - 覆盖设置接口、LDAP 用户不可删、不可改密码、手动同步。
- 修改: `cmd/server/main.go`
  - 注册 `/api/admin/ldap/sync`。
- 修改: `cmd/server/maintenance.go`
  - 增加 LDAP 定时同步调度。

### 前端核心改动

- 修改: `frontend/src/views/AdminView.vue`
  - 增加 LDAP 配置区、同步按钮、用户来源展示、LDAP 用户限制编辑。
- 修改: `frontend/src/utils/api.js`
  - 复用 `apiFetch`，减少后台页重复 fetch 逻辑。

### 验证命令

- 后端单测: `go test ./internal/store ./internal/ldap ./cmd/server`
- 全量后端: `go test ./...`
- 前端构建: `cd frontend && npm run build`

---

### Task 1: 扩展数据库 schema 和 store 层

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/users.go`
- Modify: `internal/store/settings.go`
- Test: `internal/store/users_ldap_test.go`

- [ ] **Step 1: 先写 store 层失败测试，锁定 LDAP 字段和 upsert 语义**

```go
package store

import (
	"context"
	"testing"
)

func TestOpen_MigratesLDAPUserColumns(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/cups-web.db"

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	err = s.WithTx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users (
				username, password_hash, role, protected,
				auth_source, ldap_uid, ldap_dn, ldap_sync_enabled, ldap_present,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"ldap-user", "", RoleUser, 0,
			"ldap", "u-1", "cn=u1,dc=example,dc=com", 1, 1,
			nowUTC(), nowUTC(),
		)
		return err
	})
	if err != nil {
		t.Fatalf("expected LDAP columns to exist, got err = %v", err)
	}
}

func TestUpsertLDAPUser_PreservesLocalRoleAndProfile(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	var got User
	err = s.WithTx(ctx, false, func(tx *sql.Tx) error {
		created, err := UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username:    "alice",
			LDAPUID:     "alice",
			LDAPDN:      "cn=alice,dc=example,dc=com",
			ContactName: "Alice LDAP",
			Email:       "alice@example.com",
			Phone:       "123",
			DefaultRole: RoleUser,
		})
		if err != nil {
			return err
		}

		_, err = UpdateUser(ctx, tx, UpdateUserInput{
			ID:          created.ID,
			Username:    created.Username,
			Role:        RoleAdmin,
			ContactName: "Local Name",
			Phone:       "999",
			Email:       "local@example.com",
		})
		if err != nil {
			return err
		}

		got, err = UpsertLDAPUser(ctx, tx, UpsertLDAPUserInput{
			Username:    "alice",
			LDAPUID:     "alice",
			LDAPDN:      "cn=alice,dc=example,dc=com",
			ContactName: "Alice From LDAP",
			Email:       "alice-new@example.com",
			Phone:       "555",
			DefaultRole: RoleUser,
		})
		return err
	})
	if err != nil {
		t.Fatalf("UpsertLDAPUser() err = %v", err)
	}

	if got.Role != RoleAdmin {
		t.Fatalf("Role = %q, want %q", got.Role, RoleAdmin)
	}
	if got.ContactName != "Local Name" {
		t.Fatalf("ContactName = %q, want local value", got.ContactName)
	}
	if got.Email != "local@example.com" {
		t.Fatalf("Email = %q, want local value", got.Email)
	}
}
```

- [ ] **Step 2: 运行 store 测试，确认它先失败**

Run: `go test ./internal/store -run 'TestOpen_MigratesLDAPUserColumns|TestUpsertLDAPUser_PreservesLocalRoleAndProfile' -v`

Expected: FAIL，提示缺少 LDAP 列、`UpsertLDAPUser` 未定义，或 `User` 结构缺少新字段。

- [ ] **Step 3: 最小实现 schema、settings 和用户持久化**

`internal/store/store.go` 新增字段和常量：

```go
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	SettingRetentionDays         = "retention_days"
	SettingLDAPEnabled           = "ldap_enabled"
	SettingLDAPURL               = "ldap_url"
	SettingLDAPBindDN            = "ldap_bind_dn"
	SettingLDAPBindPassword      = "ldap_bind_password"
	SettingLDAPBaseDN            = "ldap_base_dn"
	SettingLDAPUserFilter        = "ldap_user_filter"
	SettingLDAPLoginAttr         = "ldap_login_attr"
	SettingLDAPDisplayNameAttr   = "ldap_display_name_attr"
	SettingLDAPEmailAttr         = "ldap_email_attr"
	SettingLDAPPhoneAttr         = "ldap_phone_attr"
	SettingLDAPSyncIntervalMins  = "ldap_sync_interval_minutes"
	SettingLDAPLastSyncStartedAt = "ldap_last_sync_started_at"
	SettingLDAPLastSyncFinishedAt = "ldap_last_sync_finished_at"
	SettingLDAPLastSyncStatus    = "ldap_last_sync_status"
	SettingLDAPLastSyncMessage   = "ldap_last_sync_message"
	SettingLDAPLastSyncCount     = "ldap_last_sync_count"
)
```

```go
if err := addColumnIfMissing(ctx, s.DB, "users", "auth_source TEXT NOT NULL DEFAULT 'local'"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_dn TEXT"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_uid TEXT"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_sync_enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "ldap_present INTEGER NOT NULL DEFAULT 1"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "last_ldap_sync_at TEXT"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
if err := addColumnIfMissing(ctx, s.DB, "users", "last_login_at TEXT"); err != nil {
	return fmt.Errorf("migrate: %w", err)
}
```

`internal/store/users.go` 扩展结构：

```go
type User struct {
	ID              int64
	Username        string
	PasswordHash    string
	Role            string
	Protected       bool
	ContactName     string
	Phone           string
	Email           string
	AuthSource      string
	LDAPDN          string
	LDAPUID         string
	LDAPSyncEnabled bool
	LDAPPresent     bool
	LastLDAPSyncAt  string
	LastLoginAt     string
	CreatedAt       string
	UpdatedAt       string
}
```

`internal/store/users.go` 新增 upsert 输入和方法：

```go
type UpsertLDAPUserInput struct {
	Username    string
	LDAPUID     string
	LDAPDN      string
	ContactName string
	Phone       string
	Email       string
	DefaultRole string
}

func UpsertLDAPUser(ctx context.Context, tx *sql.Tx, input UpsertLDAPUserInput) (User, error) {
	now := nowUTC()
	current, err := GetUserByLDAPUIDOrDN(ctx, tx, input.LDAPUID, input.LDAPDN)
	if err == sql.ErrNoRows {
		return CreateUser(ctx, tx, CreateUserInput{
			Username:        input.Username,
			PasswordHash:    "",
			Role:            normalizeRoleOrDefault(input.DefaultRole, RoleUser),
			Protected:       false,
			ContactName:     input.ContactName,
			Phone:           input.Phone,
			Email:           input.Email,
			AuthSource:      "ldap",
			LDAPUID:         input.LDAPUID,
			LDAPDN:          input.LDAPDN,
			LDAPSyncEnabled: true,
			LDAPPresent:     true,
			LastLDAPSyncAt:  now,
		})
	}
	if err != nil {
		return User{}, err
	}

	contactName := current.ContactName
	if contactName == "" {
		contactName = input.ContactName
	}
	phone := current.Phone
	if phone == "" {
		phone = input.Phone
	}
	email := current.Email
	if email == "" {
		email = input.Email
	}

	_, err = tx.ExecContext(ctx, `UPDATE users SET
		username = ?, auth_source = 'ldap', ldap_uid = ?, ldap_dn = ?,
		ldap_sync_enabled = 1, ldap_present = 1, last_ldap_sync_at = ?,
		contact_name = ?, phone = ?, email = ?, updated_at = ?
		WHERE id = ?`,
		current.Username, input.LDAPUID, input.LDAPDN,
		now, contactName, phone, email, now, current.ID,
	)
	if err != nil {
		return User{}, err
	}
	return GetUserByID(ctx, tx, current.ID)
}

func GetUserByLDAPUIDOrDN(ctx context.Context, tx *sql.Tx, ldapUID string, ldapDN string) (User, error) {
	row := tx.QueryRowContext(ctx, `SELECT
		id, username, password_hash, role, protected, contact_name, phone, email,
		auth_source, ldap_dn, ldap_uid, ldap_sync_enabled, ldap_present,
		last_ldap_sync_at, last_login_at, created_at, updated_at
		FROM users
		WHERE (ldap_uid <> '' AND ldap_uid = ?)
		   OR (ldap_dn <> '' AND ldap_dn = ?)
		ORDER BY id
		LIMIT 1`, ldapUID, ldapDN)
	return scanUser(row)
}

func TouchLastLogin(ctx context.Context, tx *sql.Tx, id int64) error {
	now := nowUTC()
	_, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func MarkMissingLDAPUsers(ctx context.Context, tx *sql.Tx, seen map[string]struct{}) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, ldap_uid FROM users WHERE auth_source = 'ldap' AND ldap_sync_enabled = 1`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var ldapUID string
		if err := rows.Scan(&id, &ldapUID); err != nil {
			return 0, err
		}
		if _, ok := seen[ldapUID]; !ok {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET ldap_present = 0, updated_at = ? WHERE id = ?`, nowUTC(), id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
```

`internal/store/settings.go` 新增：

```go
func GetSettingBool(ctx context.Context, tx *sql.Tx, key string, defaultVal bool) (bool, error) {
	raw, err := GetSettingString(ctx, tx, key, "")
	if err != nil {
		return false, err
	}
	if raw == "" {
		return defaultVal, nil
	}
	return raw == "1" || raw == "true", nil
}

func SetSettingBool(ctx context.Context, tx *sql.Tx, key string, value bool) error {
	if value {
		return SetSettingString(ctx, tx, key, "1")
	}
	return SetSettingString(ctx, tx, key, "0")
}
```

- [ ] **Step 4: 运行 store 测试，确认新行为变绿**

Run: `go test ./internal/store -run 'TestOpen_MigratesLDAPUserColumns|TestUpsertLDAPUser_PreservesLocalRoleAndProfile' -v`

Expected: PASS

- [ ] **Step 5: 提交 store 基础设施**

```bash
git add internal/store/store.go internal/store/users.go internal/store/settings.go internal/store/users_ldap_test.go
git commit -m "feat: add ldap user schema and store primitives"
```

---

### Task 2: 建立 LDAP 配置和服务层

**Files:**
- Create: `internal/ldap/config.go`
- Create: `internal/ldap/client.go`
- Create: `internal/ldap/service.go`
- Test: `internal/ldap/service_test.go`
- Modify: `go.mod`

- [ ] **Step 1: 先写 LDAP 服务失败测试，锁定认证和同步契约**

```go
package ldap

import (
	"context"
	"database/sql"
	"testing"

	"cups-web/internal/store"
)

type fakeClient struct {
	searchResult []DirectoryUser
	bindErr      error
}

func (f *fakeClient) SearchUser(ctx context.Context, cfg Config, username string) ([]DirectoryUser, error) {
	return f.searchResult, nil
}

func (f *fakeClient) BindUser(ctx context.Context, dn string, password string) error {
	return f.bindErr
}

func (f *fakeClient) SearchAllUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error) {
	return f.searchResult, nil
}

func TestAuthenticateOrProvision_CreatesLDAPUser(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	svc := NewService(s.DB, &fakeClient{
		searchResult: []DirectoryUser{{
			Username:    "alice",
			DN:          "cn=alice,dc=example,dc=com",
			UID:         "alice",
			DisplayName: "Alice",
			Email:       "alice@example.com",
		}},
	})

	user, err := svc.AuthenticateOrProvision(ctx, Config{Enabled: true, LoginAttr: "uid"}, "alice", "secret")
	if err != nil {
		t.Fatalf("AuthenticateOrProvision() err = %v", err)
	}
	if user.AuthSource != "ldap" {
		t.Fatalf("AuthSource = %q, want ldap", user.AuthSource)
	}
	if user.Role != store.RoleUser {
		t.Fatalf("Role = %q, want %q", user.Role, store.RoleUser)
	}
}

func TestSyncAll_MarksMissingUsersAsNotPresent(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir()+"/cups-web.db")
	if err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer s.Close()

	if err := s.WithTx(ctx, false, func(tx *sql.Tx) error {
		_, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
			Username:    "missing",
			LDAPUID:     "missing",
			LDAPDN:      "cn=missing,dc=example,dc=com",
			DefaultRole: store.RoleUser,
		})
		return err
	}); err != nil {
		t.Fatalf("seed LDAP user err = %v", err)
	}

	svc := NewService(s.DB, &fakeClient{searchResult: []DirectoryUser{}})
	report, err := svc.SyncAll(ctx, Config{Enabled: true, LoginAttr: "uid"})
	if err != nil {
		t.Fatalf("SyncAll() err = %v", err)
	}
	if report.MissingMarked != 1 {
		t.Fatalf("MissingMarked = %d, want 1", report.MissingMarked)
	}
}
```

- [ ] **Step 2: 运行 LDAP 服务测试，确认它先失败**

Run: `go test ./internal/ldap -run 'TestAuthenticateOrProvision_CreatesLDAPUser|TestSyncAll_MarksMissingUsersAsNotPresent' -v`

Expected: FAIL，提示包不存在、`NewService` 未定义，或 `DirectoryUser` / `Config` 缺失。

- [ ] **Step 3: 最小实现 LDAP 配置、client 抽象和服务**

`go.mod` 增加依赖：

```go
require github.com/go-ldap/ldap/v3 v3.4.8
```

`internal/ldap/config.go`：

```go
package ldap

import (
	"context"
	"database/sql"

	"cups-web/internal/store"
)

type Config struct {
	Enabled             bool
	URL                 string
	BindDN              string
	BindPassword        string
	BaseDN              string
	UserFilter          string
	LoginAttr           string
	DisplayNameAttr     string
	EmailAttr           string
	PhoneAttr           string
	SyncIntervalMinutes int64
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.URL == "" || c.BaseDN == "" || c.LoginAttr == "" {
		return ErrInvalidConfig
	}
	return nil
}

func LoadConfig(ctx context.Context, s *store.Store) (Config, error) {
	var cfg Config
	err := s.WithTx(ctx, true, func(tx *sql.Tx) error {
		var err error
		cfg.Enabled, err = store.GetSettingBool(ctx, tx, store.SettingLDAPEnabled, false)
		if err != nil {
			return err
		}
		cfg.URL, err = store.GetSettingString(ctx, tx, store.SettingLDAPURL, "")
		if err != nil {
			return err
		}
		cfg.BindDN, err = store.GetSettingString(ctx, tx, store.SettingLDAPBindDN, "")
		if err != nil {
			return err
		}
		cfg.BindPassword, err = store.GetSettingString(ctx, tx, store.SettingLDAPBindPassword, "")
		if err != nil {
			return err
		}
		cfg.BaseDN, err = store.GetSettingString(ctx, tx, store.SettingLDAPBaseDN, "")
		if err != nil {
			return err
		}
		cfg.UserFilter, err = store.GetSettingString(ctx, tx, store.SettingLDAPUserFilter, "(objectClass=person)")
		if err != nil {
			return err
		}
		cfg.LoginAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPLoginAttr, "uid")
		if err != nil {
			return err
		}
		cfg.DisplayNameAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPDisplayNameAttr, "cn")
		if err != nil {
			return err
		}
		cfg.EmailAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPEmailAttr, "mail")
		if err != nil {
			return err
		}
		cfg.PhoneAttr, err = store.GetSettingString(ctx, tx, store.SettingLDAPPhoneAttr, "telephoneNumber")
		if err != nil {
			return err
		}
		cfg.SyncIntervalMinutes, err = store.GetSettingInt(ctx, tx, store.SettingLDAPSyncIntervalMins, 60)
		return err
	})
	return cfg, err
}
```

`internal/ldap/client.go`：

```go
package ldap

import "context"

type DirectoryUser struct {
	Username    string
	UID         string
	DN          string
	DisplayName string
	Email       string
	Phone       string
}

type Client interface {
	SearchUser(ctx context.Context, cfg Config, username string) ([]DirectoryUser, error)
	BindUser(ctx context.Context, dn string, password string) error
	SearchAllUsers(ctx context.Context, cfg Config) ([]DirectoryUser, error)
}
```

`internal/ldap/service.go`：

```go
package ldap

func (s *Service) AuthenticateOrProvision(ctx context.Context, cfg Config, username, password string) (store.User, error) {
	if err := cfg.Validate(); err != nil {
		return store.User{}, err
	}
	matches, err := s.client.SearchUser(ctx, cfg, username)
	if err != nil {
		return store.User{}, err
	}
	if len(matches) != 1 {
		return store.User{}, ErrInvalidCredentials
	}
	match := matches[0]
	if err := s.client.BindUser(ctx, match.DN, password); err != nil {
		return store.User{}, ErrInvalidCredentials
	}

	var user store.User
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		created, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
			Username:    match.Username,
			LDAPUID:     match.UID,
			LDAPDN:      match.DN,
			ContactName: match.DisplayName,
			Phone:       match.Phone,
			Email:       match.Email,
			DefaultRole: store.RoleUser,
		})
		if err != nil {
			return err
		}
		if err := store.TouchLastLogin(ctx, tx, created.ID); err != nil {
			return err
		}
		user, err = store.GetUserByID(ctx, tx, created.ID)
		return err
	})
	return user, err
}

func (s *Service) SyncAll(ctx context.Context, cfg Config) (SyncReport, error) {
	users, err := s.client.SearchAllUsers(ctx, cfg)
	if err != nil {
		return SyncReport{}, err
	}
	report := SyncReport{}
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		seen := map[string]struct{}{}
		for _, du := range users {
			if du.UID == "" || du.DN == "" || du.Username == "" {
				report.Skipped++
				continue
			}
			seen[du.UID] = struct{}{}
			if _, err := store.UpsertLDAPUser(ctx, tx, store.UpsertLDAPUserInput{
				Username:    du.Username,
				LDAPUID:     du.UID,
				LDAPDN:      du.DN,
				ContactName: du.DisplayName,
				Phone:       du.Phone,
				Email:       du.Email,
				DefaultRole: store.RoleUser,
			}); err != nil {
				return err
			}
			report.Upserted++
		}
		missing, err := store.MarkMissingLDAPUsers(ctx, tx, seen)
		if err != nil {
			return err
		}
		report.MissingMarked = missing
		return nil
	})
	return report, err
}
```

- [ ] **Step 4: 运行 LDAP 服务测试，确认行为变绿**

Run: `go test ./internal/ldap -run 'TestAuthenticateOrProvision_CreatesLDAPUser|TestSyncAll_MarksMissingUsersAsNotPresent' -v`

Expected: PASS

- [ ] **Step 5: 提交 LDAP 服务层**

```bash
git add go.mod go.sum internal/ldap/config.go internal/ldap/client.go internal/ldap/service.go internal/ldap/service_test.go
git commit -m "feat: add ldap service and sync primitives"
```

---

### Task 3: 改造登录流程为本地认证 + LDAP 认证并存

**Files:**
- Modify: `cmd/server/auth_handlers.go`
- Modify: `cmd/server/app.go`
- Test: `cmd/server/auth_handlers_test.go`

- [ ] **Step 1: 先写登录失败测试，锁定本地优先和 LDAP 首登建档**

```go
func TestLoginHandler_PrefersLocalUserWhenAuthSourceIsLocal(t *testing.T) {
	// 准备本地用户 alice / local-pass
	// 准备 fake LDAP 服务，即使能返回 alice，也不应被调用成功覆盖本地认证
	// 断言：local-pass 成功，wrong-pass 返回 401，而不会自动转去 LDAP
}

func TestLoginHandler_ProvisionsLDAPUserOnFirstSuccessfulLogin(t *testing.T) {
	// 准备空 users 表
	// fake LDAP 返回唯一 alice，并允许 bind
	// POST /api/login 成功后，再查询 users 表，断言 alice 被创建为 auth_source=ldap，role=user
}

func TestLoginHandler_RejectsLDAPUserWhenDirectoryMatchIsAmbiguous(t *testing.T) {
	// fake LDAP 返回两条匹配，断言登录返回 401，且本地不产生脏用户
}
```

- [ ] **Step 2: 运行 handler 测试，确认当前登录逻辑先失败**

Run: `go test ./cmd/server -run 'TestLoginHandler_' -v`

Expected: FAIL，提示测试期望的 LDAP 分支不存在，或登录后本地没有新建 LDAP 用户。

- [ ] **Step 3: 最小实现混合登录**

`cmd/server/app.go` 增加全局 LDAP 服务：

```go
package main

import (
	"cups-web/internal/ldap"
	"cups-web/internal/store"
)

var appStore *store.Store
var ldapService *ldap.Service
var uploadDir string
```

`cmd/server/auth_handlers.go` 将本地认证抽成函数并接入 LDAP：

```go
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "missing credentials")
		return
	}

	user, err := authenticateUser(r.Context(), req.Username, req.Password)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	sess := auth.Session{UserID: user.ID, Username: user.Username, Role: user.Role}
	if err := auth.SetSession(w, sess); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session error")
		return
	}
	setCSRFCookie(w)
	writeJSON(w, map[string]bool{"ok": true})
}

func authenticateUser(ctx context.Context, username, password string) (store.User, error) {
	var localUser store.User
	err := appStore.WithTx(ctx, true, func(tx *sql.Tx) error {
		found, err := store.GetUserByUsername(ctx, tx, username)
		if err == nil {
			localUser = found
		}
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	})
	if err != nil {
		return store.User{}, err
	}

	if localUser.ID != 0 && localUser.AuthSource == "local" {
		if bcrypt.CompareHashAndPassword([]byte(localUser.PasswordHash), []byte(password)) != nil {
			return store.User{}, errInvalidCredentials
		}
		_ = appStore.WithTx(ctx, false, func(tx *sql.Tx) error {
			return store.TouchLastLogin(ctx, tx, localUser.ID)
		})
		return localUser, nil
	}

	cfg, err := ldap.LoadConfig(ctx, appStore)
	if err != nil || !cfg.Enabled {
		return store.User{}, errInvalidCredentials
	}
	return ldapService.AuthenticateOrProvision(ctx, cfg, username, password)
}
```

- [ ] **Step 4: 运行登录测试，确认行为变绿**

Run: `go test ./cmd/server -run 'TestLoginHandler_' -v`

Expected: PASS

- [ ] **Step 5: 提交混合登录改动**

```bash
git add cmd/server/app.go cmd/server/auth_handlers.go cmd/server/auth_handlers_test.go
git commit -m "feat: support hybrid local and ldap login"
```

---

### Task 4: 扩展管理员用户接口、设置接口和手动同步接口

**Files:**
- Modify: `cmd/server/admin_handlers.go`
- Modify: `cmd/server/main.go`
- Test: `cmd/server/admin_handlers_test.go`

- [ ] **Step 1: 先写管理员接口失败测试，锁定 LDAP 用户限制**

```go
func TestAdminUpdateUser_RejectsPasswordChangeForLDAPUser(t *testing.T) {
	// 准备 auth_source=ldap 用户
	// PUT /api/admin/users/{id} 携带 password
	// 断言返回 400，错误文案为 "ldap user password cannot be changed"
}

func TestAdminDeleteUser_RejectsLDAPUserDeletion(t *testing.T) {
	// 准备 auth_source=ldap 用户
	// DELETE /api/admin/users/{id}
	// 断言返回 400，错误文案为 "ldap user cannot be deleted"
}

func TestAdminLDAPSync_TriggersServiceSync(t *testing.T) {
	// fake LDAP service 记录调用次数
	// POST /api/admin/ldap/sync
	// 断言返回 200，且同步状态写入 settings
}
```

- [ ] **Step 2: 运行管理员接口测试，确认先失败**

Run: `go test ./cmd/server -run 'TestAdmin(UpdateUser|DeleteUser|LDAPSync)_' -v`

Expected: FAIL，提示当前接口允许删除/改密码，且 `/api/admin/ldap/sync` 路由不存在。

- [ ] **Step 3: 最小实现用户响应扩展、设置扩展和手动同步路由**

`cmd/server/main.go` 注册手动同步接口：

```go
admin.HandleFunc("/ldap/sync", adminLDAPSyncHandler).Methods("POST")
```

`cmd/server/admin_handlers.go` 扩展响应：

```go
type adminUserResponse struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	Role            string `json:"role"`
	Protected       bool   `json:"protected"`
	ContactName     string `json:"contactName"`
	Phone           string `json:"phone"`
	Email           string `json:"email"`
	AuthSource      string `json:"authSource"`
	LDAPSyncEnabled bool   `json:"ldapSyncEnabled"`
	LDAPPresent     bool   `json:"ldapPresent"`
	LastLDAPSyncAt  string `json:"lastLdapSyncAt"`
	LastLoginAt     string `json:"lastLoginAt"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}
```

LDAP 用户更新限制：

```go
if current.AuthSource == "ldap" && strings.TrimSpace(payload.Password) != "" {
	writeJSONError(w, http.StatusBadRequest, "ldap user password cannot be changed")
	return
}
if current.AuthSource == "ldap" && payload.Username != current.Username {
	writeJSONError(w, http.StatusBadRequest, "ldap username cannot change")
	return
}
```

LDAP 用户删除限制：

```go
if user.AuthSource == "ldap" {
	writeJSONError(w, http.StatusBadRequest, "ldap user cannot be deleted")
	return
}
```

LDAP 设置和手动同步：

```go
type ldapSettingsPayload struct {
	Enabled             bool   `json:"enabled"`
	URL                 string `json:"url"`
	BindDN              string `json:"bindDn"`
	BindPassword        string `json:"bindPassword"`
	BaseDN              string `json:"baseDn"`
	UserFilter          string `json:"userFilter"`
	LoginAttr           string `json:"loginAttr"`
	DisplayNameAttr     string `json:"displayNameAttr"`
	EmailAttr           string `json:"emailAttr"`
	PhoneAttr           string `json:"phoneAttr"`
	SyncIntervalMinutes int64  `json:"syncIntervalMinutes"`
}

func adminLDAPSyncHandler(w http.ResponseWriter, r *http.Request) {
	cfg, err := ldap.LoadConfig(r.Context(), appStore)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid ldap config")
		return
	}
	report, err := ldapService.SyncAll(r.Context(), cfg)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "ldap sync failed")
		return
	}
	writeJSON(w, report)
}
```

- [ ] **Step 4: 运行管理员接口测试，确认变绿**

Run: `go test ./cmd/server -run 'TestAdmin(UpdateUser|DeleteUser|LDAPSync)_' -v`

Expected: PASS

- [ ] **Step 5: 提交管理员接口改动**

```bash
git add cmd/server/main.go cmd/server/admin_handlers.go cmd/server/admin_handlers_test.go
git commit -m "feat: add ldap admin settings and manual sync"
```

---

### Task 5: 接入后台定时同步和同步状态记录

**Files:**
- Modify: `cmd/server/maintenance.go`
- Modify: `internal/ldap/service.go`
- Test: `internal/ldap/service_test.go`

- [ ] **Step 1: 先写定时同步失败测试，锁定“关闭时不跑、错误不 panic”**

```go
func TestRunPeriodicLDAPSync_SkipsWhenDisabled(t *testing.T) {
	// 配置 ldap_enabled=false
	// fake service 记录 SyncAll 调用次数
	// 执行一次调度函数，断言调用次数为 0
}

func TestRunPeriodicLDAPSync_RecordsFailureStatus(t *testing.T) {
	// 配置启用 LDAP
	// fake service 返回错误
	// 执行一次调度函数，断言 settings 中 ldap_last_sync_status = "error"
}
```

- [ ] **Step 2: 运行同步调度测试，确认先失败**

Run: `go test ./internal/ldap ./cmd/server -run 'TestRunPeriodicLDAPSync_' -v`

Expected: FAIL，提示调度函数不存在，或没有同步状态写回。

- [ ] **Step 3: 最小实现单次同步调度和状态落库**

`cmd/server/maintenance.go` 增加单次执行函数：

```go
func runLDAPSyncOnce(ctx context.Context, s *store.Store) error {
	cfg, err := ldap.LoadConfig(ctx, s)
	if err != nil || !cfg.Enabled || cfg.SyncIntervalMinutes <= 0 {
		return nil
	}

	if err := store.UpdateLDAPSyncStatus(ctx, s, "running", "", 0, time.Now().UTC()); err != nil {
		return err
	}

	report, err := ldapService.SyncAll(ctx, cfg)
	if err != nil {
		_ = store.UpdateLDAPSyncStatus(ctx, s, "error", err.Error(), 0, time.Now().UTC())
		return err
	}

	return store.UpdateLDAPSyncStatus(ctx, s, "ok", "", int64(report.Upserted), time.Now().UTC())
}
```

在循环中按分钟级节流：

```go
func shouldRunLDAPSync(now time.Time, last time.Time, cfg ldap.Config) bool {
	if !cfg.Enabled || cfg.SyncIntervalMinutes <= 0 {
		return false
	}
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= time.Duration(cfg.SyncIntervalMinutes)*time.Minute
}

func startMaintenance(s *store.Store, uploads string) {
	go func() {
		var lastLDAPSync time.Time
		for {
			if err := cleanupOldPrints(context.Background(), s, uploads, time.Now()); err != nil {
				log.Println("cleanup failed:", err)
			}
			cfg, err := ldap.LoadConfig(context.Background(), s)
			if err != nil {
				log.Println("load ldap config failed:", err)
			} else if shouldRunLDAPSync(time.Now(), lastLDAPSync, cfg) {
				if err := runLDAPSyncOnce(context.Background(), s); err != nil {
					log.Println("ldap sync failed:", err)
				} else {
					lastLDAPSync = time.Now()
				}
			}
			time.Sleep(1 * time.Hour)
		}
	}()
}
```

`internal/store/settings.go` 增加同步状态写回辅助：

```go
func UpdateLDAPSyncStatus(ctx context.Context, s *Store, status, message string, count int64, finishedAt time.Time) error {
	return s.WithTx(ctx, false, func(tx *sql.Tx) error {
		if err := SetSettingString(ctx, tx, SettingLDAPLastSyncStatus, status); err != nil {
			return err
		}
		if err := SetSettingString(ctx, tx, SettingLDAPLastSyncMessage, message); err != nil {
			return err
		}
		if err := SetSettingInt(ctx, tx, SettingLDAPLastSyncCount, count); err != nil {
			return err
		}
		return SetSettingString(ctx, tx, SettingLDAPLastSyncFinishedAt, finishedAt.UTC().Format(time.RFC3339))
	})
}
```

- [ ] **Step 4: 运行调度测试，确认变绿**

Run: `go test ./internal/ldap ./cmd/server -run 'TestRunPeriodicLDAPSync_' -v`

Expected: PASS

- [ ] **Step 5: 提交后台同步调度**

```bash
git add cmd/server/maintenance.go internal/ldap/service.go internal/ldap/service_test.go
git commit -m "feat: add scheduled ldap sync"
```

---

### Task 6: 更新后台前端，展示 LDAP 用户状态并支持 LDAP 设置/同步

**Files:**
- Modify: `frontend/src/views/AdminView.vue`
- Modify: `frontend/src/utils/api.js`

- [ ] **Step 1: 先写出前端数据结构和交互占位，确保 UI 改动范围清晰**

在 `frontend/src/views/AdminView.vue` 先声明新状态：

```js
const ldapSettings = ref({
  enabled: false,
  url: '',
  bindDn: '',
  bindPassword: '',
  hasBindPassword: false,
  baseDn: '',
  userFilter: '',
  loginAttr: 'uid',
  displayNameAttr: 'cn',
  emailAttr: 'mail',
  phoneAttr: 'telephoneNumber',
  syncIntervalMinutes: '60',
  lastSyncStatus: '',
  lastSyncMessage: '',
  lastSyncFinishedAt: '',
  lastSyncCount: 0
})
const syncingLDAP = ref(false)
```

- [ ] **Step 2: 运行前端构建，确认当前阶段还会因模板/字段未接线而失败或无功能**

Run: `cd frontend && npm run build`

Expected: 如果这一步已经 PASS，继续下一步；如果在引入新字段后失败，应先记录具体编译错误并据此补全实现。

- [ ] **Step 3: 最小实现 LDAP 后台页面**

`frontend/src/utils/api.js` 增加统一 JSON fetch：

```js
export async function apiFetchJSON(url, options = {}, onUnauthorized = null) {
  const resp = await apiFetch(url, options, onUnauthorized)
  const data = await resp.json().catch(() => null)
  return { resp, data }
}
```

`frontend/src/views/AdminView.vue` 用户列表新增来源列和删除限制：

```js
const userColumns = [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'username', header: '登录名' },
  { accessorKey: 'authSource', header: '来源' },
  { accessorKey: 'role', header: '角色' },
  { accessorKey: 'contactName', header: '联系人' },
  { accessorKey: 'phone', header: '电话' },
  { accessorKey: 'email', header: '邮箱' },
  { id: 'actions', header: '操作' }
]
```

```vue
<UButton
  size="sm"
  variant="outline"
  color="error"
  icon="i-lucide-trash-2"
  :disabled="row.original.username === 'admin' || row.original.authSource === 'ldap'"
  @click="confirmDelete(row.original)"
>
  删除
</UButton>
```

LDAP 设置卡片：

```vue
<UCard>
  <template #header>
    <h2 class="text-xl font-bold flex items-center gap-2">
      <UIcon name="i-lucide-network" class="w-5 h-5" />
      LDAP 设置
    </h2>
  </template>
  <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
    <UCheckbox v-model="ldapSettings.enabled" label="启用 LDAP" />
    <UInput v-model="ldapSettings.url" placeholder="ldaps://ldap.example.com:636" />
    <UInput v-model="ldapSettings.bindDn" placeholder="cn=admin,dc=example,dc=com" />
    <UInput v-model="ldapSettings.bindPassword" type="password" :placeholder="ldapSettings.hasBindPassword ? '已设置，留空表示不修改' : 'Bind 密码'" />
    <UInput v-model="ldapSettings.baseDn" placeholder="dc=example,dc=com" />
    <UInput v-model="ldapSettings.userFilter" placeholder="(objectClass=person)" />
    <UInput v-model="ldapSettings.loginAttr" placeholder="uid" />
    <UInput v-model="ldapSettings.displayNameAttr" placeholder="cn" />
    <UInput v-model="ldapSettings.emailAttr" placeholder="mail" />
    <UInput v-model="ldapSettings.phoneAttr" placeholder="telephoneNumber" />
    <UInput v-model="ldapSettings.syncIntervalMinutes" type="number" placeholder="60" />
  </div>
  <div class="flex gap-2 mt-4">
    <UButton color="primary" :loading="savingSettings" @click="saveSettings">保存 LDAP 设置</UButton>
    <UButton variant="outline" :loading="syncingLDAP" @click="syncLDAP">立即同步</UButton>
  </div>
</UCard>
```

- [ ] **Step 4: 运行前端构建，确认 UI 代码可编译**

Run: `cd frontend && npm run build`

Expected: PASS

- [ ] **Step 5: 提交前端 LDAP 后台**

```bash
git add frontend/src/views/AdminView.vue frontend/src/utils/api.js
git commit -m "feat: add ldap admin management ui"
```

---

### Task 7: 跑整体验证并补 README/AGENTS 中的最小说明

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`

- [ ] **Step 1: 先写最小文档增量，避免功能完成后没有配置说明**

`README.md` 增加 LDAP 段落：

```md
## LDAP 用户同步

管理员可在后台“系统设置”中配置 LDAP 连接参数，启用后支持：

- LDAP 用户首次登录自动建档
- 管理员手动同步 LDAP 用户
- 按配置周期定时同步 LDAP 用户

注意：

- LDAP 用户角色仍在本地后台维护
- LDAP 用户不能在系统内修改密码
- 第一版不支持删除 LDAP 用户，本地会保留历史打印记录
```

`AGENTS.md` 在认证/API 章节补充：

```md
- `POST /api/admin/ldap/sync`：管理员手动触发 LDAP 同步
- `users.auth_source`：`local` / `ldap`
- `users.ldap_present = 0`：表示该 LDAP 用户已不在最近一次目录同步结果中，不能继续通过 LDAP 登录
```

- [ ] **Step 2: 跑后端全量测试**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: 跑前端构建**

Run: `cd frontend && npm run build`

Expected: PASS

- [ ] **Step 4: 手工回归关键路径**

Run:

```bash
go test ./cmd/server -run 'TestLoginHandler_|TestAdmin(UpdateUser|DeleteUser|LDAPSync)_' -v
go test ./internal/ldap -run 'TestAuthenticateOrProvision_CreatesLDAPUser|TestSyncAll_MarksMissingUsersAsNotPresent|TestRunPeriodicLDAPSync_' -v
```

Expected:

- 本地用户登录不回归
- LDAP 首登建档通过
- LDAP 用户不可删、不可改密码
- 手动同步和定时同步逻辑通过

- [ ] **Step 5: 提交最终说明和验证通过的代码**

```bash
git add README.md AGENTS.md
git commit -m "docs: document ldap user management"
```

---

## 自检结论

### Spec 覆盖检查

- LDAP 用户同步到本地数据库：Task 1、Task 2、Task 5
- LDAP 登录：Task 2、Task 3
- 本地角色管理：Task 1、Task 4
- 管理员手动同步：Task 4、Task 6
- 定时同步：Task 5
- LDAP 用户不可删除：Task 4、Task 6
- 同步状态通过设置接口返回：Task 4、Task 6
- 本地用户与默认 `admin` 不回归：Task 3、Task 4、Task 7

### 占位词检查

- 计划中没有 `TODO`、`TBD`、`implement later` 一类占位词。
- 每个任务都包含具体文件、代码骨架、测试命令和提交命令。

### 类型一致性检查

- `auth_source` / `AuthSource`
- `ldap_present` / `LDAPPresent`
- `last_ldap_sync_at` / `LastLDAPSyncAt`
- `POST /api/admin/ldap/sync`

以上命名在任务之间保持一致，可直接作为实现基线。
