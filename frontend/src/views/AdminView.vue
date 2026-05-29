<template>
  <div class="p-3 sm:p-4 md:p-6 space-y-4 md:space-y-6">
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-6">
      <UCard>
        <template #header>
          <h2 class="text-xl font-bold flex items-center gap-2">
            <UIcon name="i-lucide-users" class="w-5 h-5" />
            用户管理
          </h2>
        </template>
        <UForm @submit="saveUser" :state="form" class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <div>
            <UInput
              v-model="form.username"
              :disabled="form.protected || isLDAPEditing"
              placeholder="登录名"
              :color="formErrors.username ? 'error' : undefined"
            />
            <p v-if="formErrors.username" class="text-xs text-error mt-1">{{ formErrors.username }}</p>
          </div>
          <div v-if="!isLDAPEditing">
            <UInput
              type="password"
              v-model="form.password"
              :placeholder="isEditing ? '留空不修改密码' : '密码'"
              :color="formErrors.password ? 'error' : undefined"
            />
            <p v-if="formErrors.password" class="text-xs text-error mt-1">{{ formErrors.password }}</p>
          </div>
          <div v-else class="rounded-lg border border-default bg-muted/40 px-3 py-2 text-sm text-muted">
            LDAP 用户密码需在目录服务中维护，后台不提供修改。
          </div>
          <USelect
            v-model="form.role"
            :disabled="form.protected"
            :items="roleItems"
            value-key="value"
            label-key="label"
          />
          <UInput v-model="form.contactName" placeholder="联系人" />
          <UInput v-model="form.phone" placeholder="联系电话" />
          <div>
            <UInput v-model="form.email" placeholder="邮箱" :color="formErrors.email ? 'error' : undefined" />
            <p v-if="formErrors.email" class="text-xs text-error mt-1">{{ formErrors.email }}</p>
          </div>
          <div class="flex gap-2 md:col-span-2">
            <UButton type="submit" color="primary" :loading="savingUser" :disabled="savingUser">{{ isEditing ? '保存' : '新增用户' }}</UButton>
            <UButton type="button" variant="ghost" @click="resetForm">重置</UButton>
          </div>
        </UForm>

        <div class="overflow-x-auto mt-4">
          <UTable :columns="userColumns" :data="users">
            <template #authSource-cell="{ row }">
              <div class="space-y-1">
                <UBadge :color="authSourceColor(row.original.authSource)" variant="subtle" size="xs">
                  {{ authSourceLabel(row.original.authSource) }}
                </UBadge>
                <p v-if="row.original.authSource === 'ldap'" class="text-xs text-muted">
                  {{ row.original.ldapPresent ? '目录中存在' : '目录中缺失' }}
                </p>
              </div>
            </template>
            <template #actions-cell="{ row }">
              <div class="flex gap-2">
                <UButton size="sm" variant="ghost" icon="i-lucide-pencil" @click="editUser(row.original)">编辑</UButton>
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
              </div>
            </template>
          </UTable>
        </div>
      </UCard>

      <UCard>
        <template #header>
          <h2 class="text-xl font-bold flex items-center gap-2">
            <UIcon name="i-lucide-file-text" class="w-5 h-5" />
            打印记录
          </h2>
        </template>
        <div class="flex flex-wrap gap-3 items-end mb-4">
          <UInput v-model="printFilters.username" placeholder="用户名" />
          <UInput type="date" v-model="printFilters.start" />
          <UInput type="date" v-model="printFilters.end" />
          <UButton variant="outline" @click="loadPrintRecords" icon="i-lucide-search">查询</UButton>
        </div>
        <div class="overflow-x-auto">
          <UTable :columns="printColumns" :data="printRecords">
            <template #download-cell="{ row }">
              <UButton size="xs" variant="ghost" icon="i-lucide-download" @click="downloadFile(row.original.id)">下载</UButton>
            </template>
          </UTable>
        </div>
      </UCard>
    </div>

    <UCard>
      <template #header>
        <h2 class="text-xl font-bold flex items-center gap-2">
          <UIcon name="i-lucide-settings" class="w-5 h-5" />
          系统设置
        </h2>
      </template>
      <div class="grid grid-cols-1 md:grid-cols-4 gap-3 items-end">
        <div>
          <label class="block text-sm font-medium mb-1">自动清理天数</label>
          <UInput type="number" step="1" v-model="settings.retentionDays" placeholder="例如 30" />
        </div>
      </div>
      <div class="text-sm text-muted mt-2">自动清理会删除打印记录与文件，并压缩数据库。</div>

      <div class="border-t border-default mt-5 pt-5 space-y-4">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h3 class="text-base font-semibold">LDAP 设置</h3>
            <p class="text-sm text-muted mt-1">配置目录登录、定时同步和用户资料字段映射。</p>
          </div>
          <label class="inline-flex items-center gap-2 text-sm font-medium">
            <UCheckbox :model-value="settings.ldap.enabled" @update:model-value="settings.ldap.enabled = !!$event" />
            <span>启用 LDAP</span>
          </label>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <div>
            <label class="block text-sm font-medium mb-1">LDAP URL</label>
            <UInput v-model="settings.ldap.url" placeholder="ldap://ldap.example.com:389" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Base DN</label>
            <UInput v-model="settings.ldap.baseDn" placeholder="dc=example,dc=com" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Bind DN</label>
            <UInput v-model="settings.ldap.bindDn" placeholder="cn=service,dc=example,dc=com" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Bind Password</label>
            <UInput
              type="password"
              v-model="settings.ldap.bindPassword"
              :placeholder="settings.ldap.hasBindPassword ? '留空保持当前密码' : '输入绑定密码'"
            />
            <p class="text-xs text-muted mt-1">
              {{ settings.ldap.hasBindPassword ? '服务端已保存绑定密码，留空不会覆盖。' : '当前未保存绑定密码。' }}
            </p>
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">User Filter</label>
            <UInput v-model="settings.ldap.userFilter" placeholder="(objectClass=person)" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Login Attr</label>
            <UInput v-model="settings.ldap.loginAttr" placeholder="uid" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Display Name Attr</label>
            <UInput v-model="settings.ldap.displayNameAttr" placeholder="cn" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Email Attr</label>
            <UInput v-model="settings.ldap.emailAttr" placeholder="mail" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Phone Attr</label>
            <UInput v-model="settings.ldap.phoneAttr" placeholder="telephoneNumber" />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">同步间隔（分钟）</label>
            <UInput type="number" step="1" min="1" v-model="settings.ldap.syncIntervalMinutes" placeholder="60" />
          </div>
        </div>

        <div class="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_auto] gap-3 items-start">
          <div class="rounded-xl border border-default bg-muted/30 p-4 space-y-3">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium">同步状态</span>
              <UBadge :color="ldapSyncStatusColor(settings.ldapSyncStatus.lastStatus)" variant="subtle" size="xs">
                {{ ldapSyncStatusLabel(settings.ldapSyncStatus.lastStatus) }}
              </UBadge>
            </div>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-2 text-sm">
              <div>
                <div class="text-muted">最近开始</div>
                <div>{{ formatDateTime(settings.ldapSyncStatus.lastStartedAt) }}</div>
              </div>
              <div>
                <div class="text-muted">最近完成</div>
                <div>{{ formatDateTime(settings.ldapSyncStatus.lastFinishedAt) }}</div>
              </div>
              <div>
                <div class="text-muted">最近同步新增/更新</div>
                <div>{{ settings.ldapSyncStatus.lastCount || 0 }}</div>
              </div>
              <div>
                <div class="text-muted">摘要</div>
                <div>{{ settings.ldapSyncStatus.lastMessage || '暂无同步记录' }}</div>
              </div>
            </div>
          </div>
          <div class="flex gap-2">
            <UButton
              variant="outline"
              icon="i-lucide-refresh-cw"
              :loading="syncingLDAP"
              :disabled="syncingLDAP || savingSettings"
              @click="syncLDAP"
            >
              立即同步
            </UButton>
            <UButton
              color="primary"
              icon="i-lucide-save"
              :loading="savingSettings"
              :disabled="savingSettings || syncingLDAP"
              @click="saveSettings"
            >
              保存设置
            </UButton>
          </div>
        </div>
      </div>
    </UCard>

    <UModal v-model:open="showDeleteModal">
      <template #content>
        <div class="p-6 space-y-4">
          <h3 class="text-lg font-semibold">确认删除</h3>
          <p>确定要删除用户 <strong>{{ pendingDeleteUser?.username }}</strong> 吗？</p>
          <p class="text-sm text-muted">此操作不可撤销。</p>
          <div class="flex justify-end gap-2">
            <UButton variant="ghost" @click="showDeleteModal = false">取消</UButton>
            <UButton color="error" :loading="!!deletingUserId" @click="executeDelete">确认删除</UButton>
          </div>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { apiDelete, apiGetJSON, apiSendJSON } from '../utils/api'

const toast = useToast()
const emit = defineEmits(['logout'])

const users = ref([])
const form = ref(createEmptyUserForm())
const printFilters = ref({ username: '', start: '', end: '' })
const printRecords = ref([])
const settings = ref(createDefaultSettings())

const savingUser = ref(false)
const savingSettings = ref(false)
const syncingLDAP = ref(false)
const deletingUserId = ref(null)
const pendingDeleteUser = ref(null)
const showDeleteModal = ref(false)
const formErrors = ref({})

const isEditing = computed(() => !!form.value.id)
const isLDAPEditing = computed(() => isEditing.value && form.value.authSource === 'ldap')

const roleItems = [
  { label: '普通用户', value: 'user' },
  { label: '管理员', value: 'admin' }
]

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

const printColumns = [
  { accessorKey: 'createdAt', header: '时间' },
  { accessorKey: 'username', header: '用户' },
  { accessorKey: 'filename', header: '文件' },
  { accessorKey: 'pages', header: '页数' },
  { accessorKey: 'status', header: '状态' },
  { id: 'download', header: '下载' }
]

function createEmptyUserForm() {
  return {
    id: null,
    username: '',
    password: '',
    role: 'user',
    protected: false,
    authSource: 'local',
    contactName: '',
    phone: '',
    email: ''
  }
}

function createDefaultSettings() {
  return {
    retentionDays: '0',
    ldap: {
      enabled: false,
      url: '',
      bindDn: '',
      bindPassword: '',
      hasBindPassword: false,
      baseDn: '',
      userFilter: '(objectClass=person)',
      loginAttr: 'uid',
      displayNameAttr: 'cn',
      emailAttr: 'mail',
      phoneAttr: 'telephoneNumber',
      syncIntervalMinutes: '60'
    },
    ldapSyncStatus: {
      lastStartedAt: '',
      lastFinishedAt: '',
      lastStatus: '',
      lastMessage: '',
      lastCount: 0
    }
  }
}

function authSourceLabel(source) {
  return source === 'ldap' ? 'LDAP' : '本地'
}

function authSourceColor(source) {
  return source === 'ldap' ? 'primary' : 'neutral'
}

function ldapSyncStatusLabel(status) {
  switch (status) {
    case 'running':
      return '同步中'
    case 'success':
      return '成功'
    case 'error':
      return '失败'
    default:
      return '未执行'
  }
}

function ldapSyncStatusColor(status) {
  switch (status) {
    case 'running':
      return 'warning'
    case 'success':
      return 'success'
    case 'error':
      return 'error'
    default:
      return 'neutral'
  }
}

function formatDateTime(value) {
  if (!value) {
    return '暂无'
  }

  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }

  return date.toLocaleString('zh-CN', {
    hour12: false
  })
}

function handleUnauthorized() {
  emit('logout')
}

function validateForm() {
  formErrors.value = {}
  if (!form.value.username.trim()) {
    formErrors.value.username = '用户名不能为空'
  }
  if (!isEditing.value && !form.value.password) {
    formErrors.value.password = '新用户必须设置密码'
  }
  if (form.value.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.value.email)) {
    formErrors.value.email = '邮箱格式无效'
  }
  return Object.keys(formErrors.value).length === 0
}

function resetForm() {
  form.value = createEmptyUserForm()
  formErrors.value = {}
}

function editUser(user) {
  form.value = {
    id: user.id,
    username: user.username,
    password: '',
    role: user.role,
    protected: user.protected || user.username === 'admin',
    authSource: user.authSource || 'local',
    contactName: user.contactName || '',
    phone: user.phone || '',
    email: user.email || ''
  }
  formErrors.value = {}
}

async function loadUsers() {
  try {
    users.value = await apiGetJSON('/api/admin/users', handleUnauthorized) || []
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  }
}

async function saveUser() {
  if (!validateForm()) return
  savingUser.value = true
  try {
    const payload = {
      username: form.value.username,
      password: form.value.password,
      role: form.value.role,
      contactName: form.value.contactName,
      phone: form.value.phone,
      email: form.value.email
    }
    const url = isEditing.value ? `/api/admin/users/${form.value.id}` : '/api/admin/users'
    const method = isEditing.value ? 'PUT' : 'POST'
    await apiSendJSON(url, method, payload, handleUnauthorized)
    toast.add({ title: isEditing.value ? '更新成功' : '创建成功', description: `用户 ${form.value.username} 已保存`, color: 'success', icon: 'i-lucide-check-circle' })
    await loadUsers()
    resetForm()
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '保存失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    savingUser.value = false
  }
}

function confirmDelete(user) {
  if (user.username === 'admin' || user.authSource === 'ldap') {
    return
  }
  pendingDeleteUser.value = user
  showDeleteModal.value = true
}

async function executeDelete() {
  const user = pendingDeleteUser.value
  if (!user) return
  deletingUserId.value = user.id
  try {
    await apiDelete(`/api/admin/users/${user.id}`, handleUnauthorized)
    toast.add({ title: '删除成功', description: `用户 ${user.username} 已删除`, color: 'success', icon: 'i-lucide-check-circle' })
    await loadUsers()
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '删除失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    deletingUserId.value = null
    showDeleteModal.value = false
    pendingDeleteUser.value = null
  }
}

function downloadFile(id) {
  window.open(`/api/print-records/${id}/file`, '_blank')
}

async function loadPrintRecords() {
  const params = new URLSearchParams()
  if (printFilters.value.username) params.set('username', printFilters.value.username)
  if (printFilters.value.start) params.set('start', printFilters.value.start)
  if (printFilters.value.end) params.set('end', printFilters.value.end)
  const query = params.toString()
  const url = query ? `/api/admin/print-records?${query}` : '/api/admin/print-records'
  try {
    printRecords.value = await apiGetJSON(url, handleUnauthorized) || []
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  }
}

async function loadSettings() {
  try {
    const data = await apiGetJSON('/api/admin/settings', handleUnauthorized)
    settings.value = {
      retentionDays: String(data?.retentionDays ?? 0),
      ldap: {
        enabled: !!data?.ldap?.enabled,
        url: data?.ldap?.url || '',
        bindDn: data?.ldap?.bindDn || '',
        bindPassword: '',
        hasBindPassword: !!data?.ldap?.hasBindPassword,
        baseDn: data?.ldap?.baseDn || '',
        userFilter: data?.ldap?.userFilter || '(objectClass=person)',
        loginAttr: data?.ldap?.loginAttr || 'uid',
        displayNameAttr: data?.ldap?.displayNameAttr || 'cn',
        emailAttr: data?.ldap?.emailAttr || 'mail',
        phoneAttr: data?.ldap?.phoneAttr || 'telephoneNumber',
        syncIntervalMinutes: String(data?.ldap?.syncIntervalMinutes ?? 60)
      },
      ldapSyncStatus: {
        lastStartedAt: data?.ldapSyncStatus?.lastStartedAt || '',
        lastFinishedAt: data?.ldapSyncStatus?.lastFinishedAt || '',
        lastStatus: data?.ldapSyncStatus?.lastStatus || '',
        lastMessage: data?.ldapSyncStatus?.lastMessage || '',
        lastCount: data?.ldapSyncStatus?.lastCount || 0
      }
    }
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  }
}

async function saveSettings() {
  savingSettings.value = true
  try {
    const retentionDays = Number.parseInt(settings.value.retentionDays || '0', 10)
    const syncIntervalMinutes = Number.parseInt(settings.value.ldap.syncIntervalMinutes || '0', 10)
    const payload = {
      retentionDays: Number.isNaN(retentionDays) ? 0 : retentionDays,
      ldap: {
        enabled: settings.value.ldap.enabled,
        url: settings.value.ldap.url,
        bindDn: settings.value.ldap.bindDn,
        bindPassword: settings.value.ldap.bindPassword,
        baseDn: settings.value.ldap.baseDn,
        userFilter: settings.value.ldap.userFilter,
        loginAttr: settings.value.ldap.loginAttr,
        displayNameAttr: settings.value.ldap.displayNameAttr,
        emailAttr: settings.value.ldap.emailAttr,
        phoneAttr: settings.value.ldap.phoneAttr,
        syncIntervalMinutes: Number.isNaN(syncIntervalMinutes) ? 0 : syncIntervalMinutes
      }
    }
    await apiSendJSON('/api/admin/settings', 'PUT', payload, handleUnauthorized)
    toast.add({ title: '保存成功', description: '系统设置已更新', color: 'success', icon: 'i-lucide-check-circle' })
    await loadSettings()
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '保存失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    savingSettings.value = false
  }
}

async function syncLDAP() {
  syncingLDAP.value = true
  let shouldRefresh = true
  try {
    const data = await apiSendJSON('/api/admin/ldap/sync', 'POST', {}, handleUnauthorized)
    const report = data?.report || {}
    toast.add({
      title: '同步成功',
      description: `已同步 upserted=${report.upserted || 0} skipped=${report.skipped || 0} missingMarked=${report.missingMarked || 0}`,
      color: 'success',
      icon: 'i-lucide-check-circle'
    })
  } catch (error) {
    if (error.status === 401) {
      shouldRefresh = false
    } else {
      toast.add({ title: '同步失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    syncingLDAP.value = false
  }

  if (shouldRefresh) {
    await Promise.allSettled([loadUsers(), loadSettings()])
  }
}

onMounted(async () => {
  await Promise.all([loadUsers(), loadPrintRecords(), loadSettings()])
})
</script>
