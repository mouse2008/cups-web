const SUBJECT_TYPE_ROLE = 'role'
const SUBJECT_TYPE_USER = 'user'
const SUBJECT_TYPE_GROUP = 'group'
const EFFECT_ALLOW = 'allow'
const EFFECT_DENY = 'deny'
const DEFAULT_USER_ITEM_LIMIT = 100
const DEFAULT_GROUP_ITEM_LIMIT = 100

export function createDefaultPrinterACLRule() {
  return {
    id: null,
    subjectType: SUBJECT_TYPE_ROLE,
    subjectRole: 'user',
    subjectUserId: null,
    subjectGroupId: null,
    effect: EFFECT_ALLOW
  }
}

export function normalizePrinterACLRule(rule = {}) {
  const subjectType = normalizeSubjectType(rule.subjectType)
  const effect = normalizeEffect(rule.effect)

  return {
    id: rule.id ?? null,
    subjectType,
    subjectRole: subjectType === SUBJECT_TYPE_ROLE ? normalizeRole(rule.subjectRole) : '',
    subjectUserId: subjectType === SUBJECT_TYPE_USER ? normalizeEntityId(rule.subjectUserId) : null,
    subjectGroupId: subjectType === SUBJECT_TYPE_GROUP ? normalizeEntityId(rule.subjectGroupId) : null,
    effect
  }
}

export function buildPrinterACLUpdatePayload(rules) {
  return {
    rules: rules.map((rule, index) => {
      const normalized = normalizePrinterACLRule(rule)
      const prefix = `第 ${index + 1} 条规则`

      if (normalized.effect !== EFFECT_ALLOW && normalized.effect !== EFFECT_DENY) {
        throw new Error(`${prefix}效果非法，无法构造提交数据`)
      }

      if (normalized.subjectType === SUBJECT_TYPE_ROLE) {
        return {
          subjectType: normalized.subjectType,
          subjectRole: normalized.subjectRole,
          effect: normalized.effect
        }
      }

      if (normalized.subjectType === SUBJECT_TYPE_USER) {
        return {
          subjectType: normalized.subjectType,
          subjectUserId: normalized.subjectUserId,
          effect: normalized.effect
        }
      }

      if (normalized.subjectType === SUBJECT_TYPE_GROUP) {
        return {
          subjectType: normalized.subjectType,
          subjectGroupId: normalized.subjectGroupId,
          effect: normalized.effect
        }
      }

      throw new Error(`${prefix}主体类型非法，无法构造提交数据`)
    })
  }
}

export function validatePrinterACLConfig(printerUri, rules) {
  const errors = []
  const seen = new Set()

  if (!String(printerUri || '').trim()) {
    errors.push('请先选择打印机')
  }

  rules.forEach((rule, index) => {
    const normalized = normalizePrinterACLRule(rule)
    const prefix = `第 ${index + 1} 条规则：`

    if (normalized.subjectType === SUBJECT_TYPE_ROLE) {
      if (!normalized.subjectRole) {
        errors.push(`${prefix}角色规则必须选择角色`)
        return
      }
    } else if (normalized.subjectType === SUBJECT_TYPE_USER) {
      if (!Number.isInteger(normalized.subjectUserId) || normalized.subjectUserId <= 0) {
        errors.push(`${prefix}用户规则必须选择用户`)
        return
      }
    } else if (normalized.subjectType === SUBJECT_TYPE_GROUP) {
      if (!Number.isInteger(normalized.subjectGroupId) || normalized.subjectGroupId <= 0) {
        errors.push(`${prefix}分组规则必须选择分组`)
        return
      }
    } else {
      errors.push(`${prefix}主体类型只能是 role、user 或 group`)
      return
    }

    if (normalized.effect !== EFFECT_ALLOW && normalized.effect !== EFFECT_DENY) {
      errors.push(`${prefix}效果只能是 allow 或 deny`)
      return
    }

    const duplicateKey = [
      normalized.subjectType,
      normalized.subjectRole || '',
      normalized.subjectUserId ?? '',
      normalized.subjectGroupId ?? '',
      normalized.effect
    ].join('|')

    if (seen.has(duplicateKey)) {
      errors.push(`${prefix}与前面的规则重复，请删除重复项`)
      return
    }
    seen.add(duplicateKey)
  })

  return {
    ok: errors.length === 0,
    errors
  }
}

export function describePrinterACLRuleTarget(rule, usersById = new Map(), groupsById = new Map()) {
  const normalized = normalizePrinterACLRule(rule)

  if (normalized.subjectType === SUBJECT_TYPE_ROLE) {
    return `角色：${roleLabel(normalized.subjectRole)}`
  }

  if (normalized.subjectType === SUBJECT_TYPE_USER) {
    const userId = normalized.subjectUserId
    const user = userId == null ? null : usersById.get(userId)
    if (user) {
      return `用户：${user.username}（ID: ${user.id}）`
    }
    if (userId != null) {
      return `用户：未知用户（ID: ${userId}）`
    }
    return '用户：未选择'
  }

  if (normalized.subjectType === SUBJECT_TYPE_GROUP) {
    const groupId = normalized.subjectGroupId
    const group = groupId == null ? null : groupsById.get(groupId)
    if (group) {
      return `分组：${group.name}（ID: ${group.id}）`
    }
    if (groupId != null) {
      return `分组：未知分组（ID: ${groupId}）`
    }
    return '分组：未选择'
  }

  return '未知主体'
}

export function roleLabel(role) {
  return role === 'admin' ? '管理员' : '普通用户'
}

export function buildPrinterACLUserItems(users = [], options = {}) {
  const keyword = String(options.keyword || '').trim().toLowerCase()
  const limit = normalizePositiveInteger(options.limit, DEFAULT_USER_ITEM_LIMIT)
  const selectedUserId = normalizeEntityId(options.selectedUserId)

  const normalizedUsers = users.map((user) => {
    const id = normalizeEntityId(user?.id)
    const username = String(user?.username || '').trim()
    const contactName = String(user?.contactName || '').trim()
    const email = String(user?.email || '').trim()
    const phone = String(user?.phone || '').trim()
    const authSource = String(user?.authSource || '').trim().toLowerCase()
    const segments = [
      username && `账号：${username}`,
      contactName && `联系人：${contactName}`,
      email && `邮箱：${email}`,
      phone && `电话：${phone}`,
      id != null && `ID：${id}`,
      authSource === 'ldap' ? '来源：ldap' : authSource ? `来源：${authSource}` : ''
    ].filter(Boolean)

    const meta = [
      contactName,
      authSource === 'ldap' ? 'LDAP' : authSource === 'local' ? '本地' : '',
      email,
      phone
    ].filter(Boolean)

    return {
      id,
      searchText: segments.join(' ').toLowerCase(),
      item: {
        label: meta.length > 0 ? `${username}（ID: ${id}） · ${meta.join(' / ')}` : `${username}（ID: ${id}）`,
        value: id
      }
    }
  }).filter((entry) => entry.id != null)

  return buildFilteredItems({
    entries: normalizedUsers,
    keyword,
    limit,
    selectedValue: selectedUserId,
    unknownLabel: (id) => `未知用户（ID: ${id}）`
  })
}

export function buildPrinterGroupMemberOptions(users = []) {
  return buildPrinterGroupMemberItems(users).items
}

export function buildPrinterGroupMemberItems(users = [], options = {}) {
  const keyword = String(options.keyword || '').trim().toLowerCase()
  const selectedUserIds = normalizeMemberItemIds(options.selectedUserIds)

  const normalizedUsers = users.map((user) => {
    const id = normalizeEntityId(user?.id)
    const username = String(user?.username || '').trim()
    const contactName = String(user?.contactName || '').trim()
    const email = String(user?.email || '').trim()
    const phone = String(user?.phone || '').trim()
    const roleText = user?.role === 'admin' ? '管理员' : '普通用户'
    const sourceText = user?.authSource === 'ldap' ? 'LDAP' : ''
    const meta = [roleText, sourceText].filter(Boolean).join(' / ')
    const displayName = contactName ? `${username} · ${contactName}` : username
    const segments = [
      username && `账号：${username}`,
      contactName && `联系人：${contactName}`,
      email && `邮箱：${email}`,
      phone && `电话：${phone}`,
      id != null && `ID：${id}`,
      roleText && `角色：${roleText}`,
      sourceText && `来源：${sourceText}`
    ].filter(Boolean)

    return {
      id,
      searchText: segments.join(' ').toLowerCase(),
      item: {
        id,
        label: `${displayName}（${meta}）`
      }
    }
  })
    .filter((entry) => entry.id != null)
    .sort((a, b) => a.item.label.localeCompare(b.item.label, 'zh-CN'))

  const matchedEntries = keyword
    ? normalizedUsers.filter((entry) => entry.searchText.includes(keyword))
    : normalizedUsers

  const items = matchedEntries.map((entry) => entry.item)
  const selectedMap = new Map(normalizedUsers.map((entry) => [entry.id, entry.item]))

  for (let index = selectedUserIds.length - 1; index >= 0; index -= 1) {
    const selectedId = selectedUserIds[index]
    const selectedItem = selectedMap.get(selectedId)
    if (selectedItem && !items.some((item) => item.id === selectedId)) {
      items.unshift(selectedItem)
    }
  }

  return {
    items,
    keyword,
    total: normalizedUsers.length,
    matched: matchedEntries.length
  }
}

export function buildPrinterACLGroupItems(groups = [], options = {}) {
  const keyword = String(options.keyword || '').trim().toLowerCase()
  const limit = normalizePositiveInteger(options.limit, DEFAULT_GROUP_ITEM_LIMIT)
  const selectedGroupId = normalizeEntityId(options.selectedGroupId)

  const normalizedGroups = groups.map((group) => {
    const id = normalizeEntityId(group?.id)
    const name = String(group?.name || '').trim()
    const description = String(group?.description || '').trim()
    const members = Array.isArray(group?.members) ? group.members : []
    const memberUsernames = members
      .map((member) => String(member?.username || '').trim())
      .filter(Boolean)
    const memberCount = Number.isInteger(group?.memberCount) ? group.memberCount : members.length
    const segments = [
      name && `名称：${name}`,
      description && `描述：${description}`,
      id != null && `ID：${id}`,
      memberUsernames.length > 0 ? `成员：${memberUsernames.join(' ')}` : '',
      `成员数：${memberCount}`
    ].filter(Boolean)

    const meta = [
      description,
      `成员 ${memberCount} 人`
    ].filter(Boolean)

    return {
      id,
      searchText: segments.join(' ').toLowerCase(),
      item: {
        label: meta.length > 0 ? `${name}（ID: ${id}） · ${meta.join(' / ')}` : `${name}（ID: ${id}）`,
        value: id
      }
    }
  }).filter((entry) => entry.id != null)

  return buildFilteredItems({
    entries: normalizedGroups,
    keyword,
    limit,
    selectedValue: selectedGroupId,
    unknownLabel: (id) => `未知分组（ID: ${id}）`
  })
}

function buildFilteredItems({ entries, keyword, limit, selectedValue, unknownLabel }) {
  const matchedEntries = keyword
    ? entries.filter((entry) => entry.searchText.includes(keyword))
    : entries

  const items = matchedEntries.slice(0, limit).map((entry) => entry.item)
  const selectedEntry = selectedValue == null
    ? null
    : entries.find((entry) => entry.id === selectedValue) || null

  if (selectedEntry && !items.some((item) => item.value === selectedEntry.item.value)) {
    items.unshift(selectedEntry.item)
  }

  if (
    selectedValue != null &&
    !selectedEntry &&
    !items.some((item) => item.value === selectedValue)
  ) {
    items.unshift({
      label: unknownLabel(selectedValue),
      value: selectedValue
    })
  }

  return {
    items,
    keyword,
    total: entries.length,
    matched: matchedEntries.length,
    truncated: matchedEntries.length > limit,
    limit
  }
}

function normalizeSubjectType(value) {
  const subjectType = String(value || '').trim().toLowerCase()
  if (subjectType === SUBJECT_TYPE_USER) {
    return SUBJECT_TYPE_USER
  }
  if (subjectType === SUBJECT_TYPE_ROLE) {
    return SUBJECT_TYPE_ROLE
  }
  if (subjectType === SUBJECT_TYPE_GROUP) {
    return SUBJECT_TYPE_GROUP
  }
  return ''
}

function normalizeEffect(value) {
  const effect = String(value || '').trim().toLowerCase()
  if (effect === EFFECT_DENY) {
    return EFFECT_DENY
  }
  if (effect === EFFECT_ALLOW) {
    return EFFECT_ALLOW
  }
  return effect
}

function normalizeRole(value) {
  const role = String(value || '').trim().toLowerCase()
  if (role === 'admin' || role === 'user') {
    return role
  }
  return ''
}

function normalizeEntityId(value) {
  if (value == null || value === '') {
    return null
  }
  const parsed = Number(value)
  return Number.isInteger(parsed) ? parsed : null
}

function normalizeMemberItemIds(values) {
  return Array.from(new Set((Array.isArray(values) ? values : [])
    .map((value) => normalizeEntityId(value))
    .filter((value) => value != null)))
}

function normalizePositiveInteger(value, fallback) {
  const parsed = Number(value)
  if (Number.isInteger(parsed) && parsed > 0) {
    return parsed
  }
  return fallback
}
