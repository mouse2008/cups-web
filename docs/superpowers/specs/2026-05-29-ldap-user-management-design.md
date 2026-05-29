# LDAP User Management Design

## Summary

Add LDAP-backed user directory support to `cups-web` while preserving the existing local-user model, session flow, and authorization checks.

The selected behavior is:

- LDAP users can be synchronized into the local database.
- LDAP authentication is supported for LDAP-backed users.
- Roles remain locally managed even for LDAP users.
- Synchronization can run on a schedule and also be triggered manually by an administrator.
- Local users and the protected default `admin` account continue to work unchanged.

This is a hybrid identity model: authentication may come from either the local database or LDAP, but the application still uses the local `users` table as the single source of truth for session identity, authorization, print ownership, and admin UI listing.

## Goals

- Support importing and refreshing users from LDAP.
- Allow LDAP users to authenticate with LDAP credentials.
- Keep local role assignment for LDAP users.
- Reuse the existing `users` table and session model so print records and admin functions stay stable.
- Allow scheduled background sync plus admin-triggered sync.
- Keep local accounts working when LDAP is disabled or temporarily unavailable.

## Non-Goals

- No role mapping from LDAP groups in the first version.
- No password reset or password change for LDAP users inside the app.
- No deletion of users from the LDAP server.
- No asynchronous task queue or progress-tracking job system in the first version.
- No automatic hard-delete of local users when they disappear from LDAP.

## Current State

The current implementation uses a single `users` table with a bcrypt `password_hash`, local role storage, and session cookies based on `securecookie`.

Relevant current behavior:

- `cmd/server/auth_handlers.go` authenticates directly against the local `users` table.
- `internal/store/users.go` assumes every user is a local row with a stored password hash.
- `cmd/server/admin_handlers.go` allows admins to create, update, and delete local users.
- `cmd/server/bootstrap.go` ensures the protected local `admin` account exists.
- `cmd/server/maintenance.go` already runs background maintenance on a timer and can host scheduled LDAP sync.

## Chosen Architecture

Use a hybrid user directory with a single local `users` table extended to support multiple authentication sources.

The local database remains authoritative for:

- session identity
- user IDs referenced by print records
- roles
- admin UI listing
- contact information after import

LDAP becomes authoritative for:

- password verification of LDAP users
- source identity attributes used to discover and refresh LDAP-backed accounts

This avoids rewriting session handling, print ownership, and user-related joins while still enabling directory-backed authentication and sync.

## Data Model Changes

Extend `users` with the following fields:

- `auth_source TEXT NOT NULL DEFAULT 'local'`
- `ldap_dn TEXT`
- `ldap_uid TEXT`
- `ldap_sync_enabled INTEGER NOT NULL DEFAULT 0`
- `ldap_present INTEGER NOT NULL DEFAULT 1`
- `last_ldap_sync_at TEXT`
- `last_login_at TEXT`

### Field Semantics

- `auth_source`
  - `local`: password stored in `password_hash`
  - `ldap`: password is verified against LDAP
- `ldap_dn`
  - the LDAP distinguished name used for bind/auth and stable matching
- `ldap_uid`
  - the application-level unique directory identifier derived from the configured login attribute
- `ldap_sync_enabled`
  - identifies rows managed by LDAP synchronization
- `ldap_present`
  - whether the user was seen in the most recent sync; used to disable stale LDAP accounts without deleting records
- `last_ldap_sync_at`
  - timestamp of last successful sync/update for that user
- `last_login_at`
  - timestamp of the last successful login

### Existing Columns

- `password_hash` remains required for schema compatibility, but LDAP users will not use it for authentication.
- `role` remains the authority for authorization and is never overwritten by LDAP sync.
- `contact_name`, `phone`, and `email` can be hydrated from LDAP and remain editable locally.

## LDAP Configuration Model

Store LDAP configuration in the existing `settings` table.

New setting keys:

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

### Configuration Behavior

- If `ldap_enabled` is false, all LDAP login and sync behavior is skipped.
- Configuration read failures or LDAP connectivity failures must not block local-user login.
- `ldap_bind_password` is stored in the same settings table as other application secrets today. This is acceptable for the first version because the project already persists session keys there, but it should be treated as sensitive in logs and API responses.

## Authentication Flow

### Local User Login

If a matching user row has `auth_source = 'local'`, continue using the current bcrypt password check.

### LDAP User Login

If LDAP is enabled, login should follow this sequence:

1. Read the submitted username and password.
2. Attempt local-user lookup by username.
3. If the user exists and `auth_source = 'local'`, use local authentication only.
4. Otherwise, search LDAP for the submitted username using `ldap_login_attr` and `ldap_user_filter`.
5. Require exactly one LDAP match.
6. Attempt LDAP bind using the matched user DN and the submitted password.
7. On success:
   - find an existing local row by `ldap_uid` or `ldap_dn`
   - if none exists, create a new local row with:
     - `auth_source = 'ldap'`
     - default `role = 'user'`
     - imported contact fields
     - sync markers populated
   - if a row exists, refresh LDAP-managed fields
   - update `last_login_at`
   - issue the existing session cookie with the local row ID and local role
8. On failure, return an authentication error without leaking directory details.

### Conflict Rules

- If a same-name local user already exists with `auth_source = 'local'`, LDAP auto-provisioning must not overwrite it.
- If multiple LDAP entries match, authentication fails.
- If LDAP lookup fails due to connectivity, local users still work and LDAP authentication returns a generic failure.

## Synchronization Strategy

Synchronization runs in three modes:

### 1. Login-Time Refresh

After a successful LDAP login, refresh only that single user's LDAP-backed profile fields and sync markers.

This keeps login responsive and avoids turning each login into a full directory scan.

### 2. Admin-Triggered Manual Sync

Add an admin-only endpoint to run a full LDAP sync on demand.

The first version should execute synchronously in the request lifecycle, matching the project's current operational style and avoiding a new background job subsystem.

### 3. Scheduled Background Sync

Extend the existing maintenance loop to run LDAP sync at the configured interval.

Behavior:

- disabled when LDAP is off
- disabled when interval is zero or invalid
- logs failures and continues future cycles
- never crashes the process on LDAP errors

## Sync Semantics

Full sync should:

1. Query LDAP using the configured base DN and user filter.
2. Build a normalized representation for each discovered directory user.
3. Match existing local rows by `ldap_uid`, falling back to `ldap_dn` when needed.
4. Create missing LDAP users as local rows with `auth_source = 'ldap'`, `ldap_sync_enabled = 1`, and default role `user`.
5. Update existing LDAP users' directory fields and sync markers.
6. Preserve local-only fields and policy-owned fields:
   - preserve `role`
   - preserve local password data for local users
   - do not convert local users into LDAP users automatically
7. Mark previously synced LDAP users that were not present in the latest directory result as `ldap_present = 0`.

### Contact Field Merge Rule

For `contact_name`, `phone`, and `email`, use "fill empty fields, do not overwrite non-empty local values" in the first version.

Reasoning:

- admins explicitly want local control over user management
- hard overwrite would erase manual corrections
- this rule is simple and predictable

## User Management UI and API Changes

### User List

Extend the admin user response with:

- `authSource`
- `ldapSyncEnabled`
- `ldapPresent`
- `lastLdapSyncAt`
- `lastLoginAt`

### Local User Creation

`POST /api/admin/users` continues to create only local users.

### User Editing

For LDAP users:

- username is read-only
- password is hidden or disabled
- role remains editable
- contact fields remain editable

For local users:

- existing behavior remains

### User Deletion

Deleting an LDAP user is not supported in the first version.

Reasoning:

- the current schema uses `print_jobs.user_id -> users.id ON DELETE CASCADE`
- deleting a user row would also delete historical print records
- introducing soft-delete semantics is safer than pretending delete is harmless

Required first-version behavior:

- local users keep the existing delete behavior
- LDAP users cannot be deleted from the admin UI
- if an LDAP user disappears from the directory, sync marks the user as `ldap_present = 0`
- users with `ldap_present = 0` cannot authenticate through LDAP
- their historical print records remain visible

### Settings UI

Add an LDAP settings section under admin system settings, including:

- enable switch
- server URL
- bind DN
- bind password
- base DN
- user filter
- login attribute
- display name attribute
- email attribute
- phone attribute
- sync interval
- manual sync action

Sensitive fields must never be echoed back in plaintext once saved. The read API should either omit `ldap_bind_password` or return a masked placeholder indicator.

## API Surface

### Existing Endpoints to Extend

- `GET /api/admin/users`
- `PUT /api/admin/users/{id}`
- `GET /api/admin/settings`
- `PUT /api/admin/settings`
- `POST /api/login`

### New Endpoints

- `POST /api/admin/ldap/sync`
  - runs an admin-triggered directory synchronization

## Settings and Status Metadata

To support admin visibility, add settings or status fields for:

- `ldap_last_sync_started_at`
- `ldap_last_sync_finished_at`
- `ldap_last_sync_status`
- `ldap_last_sync_message`
- `ldap_last_sync_count`

This avoids introducing a new table while still providing enough observability for the UI and troubleshooting. The first version returns this status data from `GET /api/admin/settings` instead of creating a separate sync-status endpoint.

## Error Handling

### Authentication Errors

- Return generic authentication failure to clients.
- Log actionable LDAP details server-side without printing secrets.

### Configuration Errors

- Saving invalid LDAP settings should fail validation at the admin API boundary where possible.
- Missing required LDAP settings should block LDAP sync and LDAP login, but not local login.

### Directory Ambiguity

- Zero results: authentication fails
- Multiple results: authentication fails
- Missing required LDAP attributes during sync: skip the record and log it

### Directory Availability

- LDAP downtime must not block service startup.
- Scheduled sync failures are logged and retried on the next cycle.

## Security Considerations

- Keep the protected local `admin` account as a break-glass path even when LDAP is enabled.
- Never allow LDAP sync to remove or rename the protected local `admin`.
- Do not expose bind passwords in API responses or logs.
- Continue using existing session and CSRF protections unchanged.
- Prefer LDAPS or StartTLS-capable configuration support during implementation.

## Migration Plan

1. Add schema columns with idempotent migrations.
2. Introduce LDAP settings accessors and validation.
3. Implement LDAP client/service layer.
4. Update login handler for hybrid auth.
5. Update admin user APIs and settings APIs.
6. Add manual sync endpoint.
7. Add scheduled sync integration in maintenance loop.
8. Update frontend admin UI and login-related presentation as needed.
9. Add tests for migration, login, sync, and admin restrictions.

## Testing Strategy

At minimum, cover:

- migration from an older database without LDAP columns
- local login remains unchanged
- first LDAP login auto-provisions a local row
- repeated LDAP login refreshes LDAP metadata without overwriting role
- LDAP users cannot change password through admin update
- manual sync creates and updates LDAP users
- full sync marks missing LDAP users as not present
- scheduled sync is disabled when LDAP is off
- LDAP errors do not break local auth or panic the maintenance loop

Use an LDAP abstraction that can be mocked in tests instead of binding handler tests directly to a live directory.

## Recommended Implementation Direction

Implement a dedicated LDAP service layer under `internal/auth` or a new `internal/ldap` package, and keep handlers thin.

The most important boundary is:

- handlers decide which flow to invoke
- LDAP service performs lookup, bind, normalization, and sync
- store layer owns durable user persistence and migration

This keeps the hybrid auth behavior testable and avoids scattering LDAP-specific logic across handlers, store functions, and maintenance code.
