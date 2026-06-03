import test from 'node:test'
import assert from 'node:assert/strict'

import {
  buildPrinterACLGroupItems,
  buildPrinterACLUpdatePayload,
  buildPrinterGroupMemberItems,
  buildPrinterGroupMemberOptions,
  createDefaultPrinterACLRule,
  describePrinterACLRuleTarget,
  validatePrinterACLConfig
} from './printerAcl.js'

test('createDefaultPrinterACLRule returns the recommended starter rule', () => {
  assert.deepEqual(createDefaultPrinterACLRule(), {
    id: null,
    subjectType: 'role',
    subjectRole: 'user',
    subjectUserId: null,
    subjectGroupId: null,
    effect: 'allow'
  })
})

test('validatePrinterACLConfig reports missing printer, incomplete rules, invalid effects, and duplicates', () => {
  const validation = validatePrinterACLConfig('', [
    {
      id: 1,
      subjectType: 'role',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: null,
      effect: 'allow'
    },
    {
      id: 2,
      subjectType: 'user',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: null,
      effect: 'allow'
    },
    {
      id: 3,
      subjectType: 'group',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: null,
      effect: 'allow'
    },
    {
      id: 4,
      subjectType: 'role',
      subjectRole: 'admin',
      subjectUserId: null,
      subjectGroupId: null,
      effect: 'maybe'
    },
    {
      id: 5,
      subjectType: 'group',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: 12,
      effect: 'deny'
    },
    {
      id: 6,
      subjectType: 'group',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: 12,
      effect: 'deny'
    }
  ])

  assert.equal(validation.ok, false)
  assert.deepEqual(validation.errors, [
    '请先选择打印机',
    '第 1 条规则：角色规则必须选择角色',
    '第 2 条规则：用户规则必须选择用户',
    '第 3 条规则：分组规则必须选择分组',
    '第 4 条规则：效果只能是 allow 或 deny',
    '第 6 条规则：与前面的规则重复，请删除重复项'
  ])
})

test('buildPrinterACLUpdatePayload keeps only fields accepted by the backend contract', () => {
  const payload = buildPrinterACLUpdatePayload([
    {
      id: 11,
      subjectType: 'role',
      subjectRole: 'admin',
      subjectUserId: 99,
      subjectGroupId: 88,
      effect: 'deny'
    },
    {
      id: 12,
      subjectType: 'user',
      subjectRole: 'user',
      subjectUserId: 42,
      subjectGroupId: 88,
      effect: 'allow'
    },
    {
      id: 13,
      subjectType: 'group',
      subjectRole: 'user',
      subjectUserId: 42,
      subjectGroupId: 7,
      effect: 'allow'
    }
  ])

  assert.deepEqual(payload, {
    rules: [
      {
        subjectType: 'role',
        subjectRole: 'admin',
        effect: 'deny'
      },
      {
        subjectType: 'user',
        subjectUserId: 42,
        effect: 'allow'
      },
      {
        subjectType: 'group',
        subjectGroupId: 7,
        effect: 'allow'
      }
    ]
  })
})

test('invalid subject types are rejected instead of being silently rewritten', () => {
  const validation = validatePrinterACLConfig('ipp://printer-1', [
    {
      id: 1,
      subjectType: 'department',
      subjectRole: '',
      subjectUserId: null,
      subjectGroupId: null,
      effect: 'allow'
    }
  ])

  assert.equal(validation.ok, false)
  assert.deepEqual(validation.errors, ['第 1 条规则：主体类型只能是 role、user 或 group'])

  assert.throws(
    () => buildPrinterACLUpdatePayload([
      {
        id: 1,
        subjectType: 'department',
        subjectRole: '',
        subjectUserId: null,
        subjectGroupId: null,
        effect: 'allow'
      }
    ]),
    /第 1 条规则主体类型非法，无法构造提交数据/
  )
})

test('describePrinterACLRuleTarget preserves unknown users and groups so admins can remove stale rules', () => {
  assert.equal(
    describePrinterACLRuleTarget(
      {
        subjectType: 'role',
        subjectRole: 'user',
        subjectUserId: null,
        subjectGroupId: null
      },
      new Map(),
      new Map()
    ),
    '角色：普通用户'
  )

  assert.equal(
    describePrinterACLRuleTarget(
      {
        subjectType: 'user',
        subjectRole: '',
        subjectUserId: 9,
        subjectGroupId: null
      },
      new Map([[9, { id: 9, username: 'alice' }]]),
      new Map()
    ),
    '用户：alice（ID: 9）'
  )

  assert.equal(
    describePrinterACLRuleTarget(
      {
        subjectType: 'user',
        subjectRole: '',
        subjectUserId: 77,
        subjectGroupId: null
      },
      new Map(),
      new Map()
    ),
    '用户：未知用户（ID: 77）'
  )

  assert.equal(
    describePrinterACLRuleTarget(
      {
        subjectType: 'group',
        subjectRole: '',
        subjectUserId: null,
        subjectGroupId: 5
      },
      new Map(),
      new Map([[5, { id: 5, name: '财务部' }]])
    ),
    '分组：财务部（ID: 5）'
  )

  assert.equal(
    describePrinterACLRuleTarget(
      {
        subjectType: 'group',
        subjectRole: '',
        subjectUserId: null,
        subjectGroupId: 88
      },
      new Map(),
      new Map()
    ),
    '分组：未知分组（ID: 88）'
  )
})

test('buildPrinterGroupMemberOptions includes contact names so admins can pick members by real name', () => {
  const options = buildPrinterGroupMemberOptions([
    {
      id: 2,
      username: 'zhangsan',
      contactName: '张三',
      role: 'user',
      authSource: 'ldap'
    },
    {
      id: 1,
      username: 'admin',
      contactName: '',
      role: 'admin',
      authSource: 'local'
    }
  ])

  assert.deepEqual(options, [
    {
      id: 1,
      label: 'admin（管理员）'
    },
    {
      id: 2,
      label: 'zhangsan · 张三（普通用户 / LDAP）'
    }
  ])
})

test('buildPrinterACLGroupItems keeps selected or unknown groups visible when filtering', () => {
  const result = buildPrinterACLGroupItems([
    {
      id: 1,
      name: '研发组',
      description: '开发测试',
      memberCount: 2,
      members: [{ username: 'alice' }, { username: 'bob' }]
    },
    {
      id: 2,
      name: '财务组',
      description: '报销审批',
      memberCount: 1,
      members: [{ username: 'cathy' }]
    }
  ], {
    keyword: '财务',
    selectedGroupId: 1,
    limit: 1
  })

  assert.equal(result.keyword, '财务')
  assert.equal(result.total, 2)
  assert.equal(result.matched, 1)
  assert.equal(result.truncated, false)
  assert.deepEqual(result.items.map((item) => item.value), [1, 2])

  const unknownResult = buildPrinterACLGroupItems([], {
    selectedGroupId: 99
  })
  assert.deepEqual(unknownResult.items, [
    { label: '未知分组（ID: 99）', value: 99 }
  ])
})

test('buildPrinterGroupMemberItems filters by username/contact/email/phone and keeps selected members visible', () => {
  const result = buildPrinterGroupMemberItems([
    {
      id: 1,
      username: 'zhangsan',
      contactName: '张三',
      email: 'zhangsan@example.com',
      phone: '13800138000',
      role: 'user',
      authSource: 'ldap'
    },
    {
      id: 2,
      username: 'lisi',
      contactName: '李四',
      email: 'lisi@example.com',
      phone: '13900139000',
      role: 'admin',
      authSource: 'local'
    }
  ], {
    keyword: '张三',
    selectedUserIds: [2]
  })

  assert.equal(result.keyword, '张三')
  assert.equal(result.total, 2)
  assert.equal(result.matched, 1)
  assert.deepEqual(result.items.map((item) => item.id), [2, 1])
  assert.match(result.items[0].label, /lisi/)
  assert.match(result.items[1].label, /张三/)

  const phoneResult = buildPrinterGroupMemberItems([
    {
      id: 3,
      username: 'wangwu',
      contactName: '王五',
      email: 'wangwu@example.com',
      phone: '13612345678',
      role: 'user',
      authSource: 'ldap'
    }
  ], {
    keyword: '1361234'
  })
  assert.deepEqual(phoneResult.items.map((item) => item.id), [3])
})
