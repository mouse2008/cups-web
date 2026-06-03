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
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h2 class="text-xl font-bold flex items-center gap-2">
              <UIcon name="i-lucide-shield-check" class="w-5 h-5" />
              打印机权限
            </h2>
            <p class="text-sm text-muted mt-1">管理某台打印机允许哪些角色、分组或单个用户使用。</p>
          </div>
          <UButton
            variant="outline"
            icon="i-lucide-refresh-cw"
            :loading="loadingPrinters || loadingPrinterACL"
            :disabled="savingPrinterACL"
            @click="refreshPrinterACLSection"
          >
            刷新数据
          </UButton>
        </div>
      </template>

      <div class="space-y-4">
        <div class="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_auto] gap-3 items-start">
          <div>
            <label class="block text-sm font-medium mb-1">选择打印机</label>
            <USelect
              :model-value="selectedPrinter"
              :items="printerItems"
              value-key="value"
              label-key="label"
              placeholder="选择要配置的打印机"
              :loading="loadingPrinters"
              :disabled="savingPrinterACL"
              @update:model-value="onPrinterSelectionChange"
            />
          </div>
          <div v-if="selectedPrinterMeta" class="rounded-xl border border-default bg-muted/20 px-3 py-2 text-sm">
            <div class="font-medium">{{ selectedPrinterMeta.name }}</div>
            <div class="text-muted break-all">{{ selectedPrinterMeta.uri }}</div>
          </div>
        </div>

        <div class="rounded-xl border border-default bg-muted/20 p-4 space-y-2 text-sm">
          <div class="font-medium text-highlighted">当前一期语义说明</div>
          <ul class="space-y-1 text-muted">
            <li>admin 默认放行，不受当前 ACL 限制。</li>
            <li>某台打印机一旦配置任意规则，即进入白名单 / 显式规则模式。</li>
            <li>支持 `role`、应用内 `group`（打印分组）与 `user` 三类主体。</li>
            <li>打印分组为系统内自定义分组，可承载 LDAP 用户，但当前不是 LDAP 原生组同步。</li>
          </ul>
        </div>

        <UAlert
          v-if="!loadingPrinters && printerItems.length === 0"
          icon="i-lucide-printer-x"
          color="warning"
          variant="soft"
          title="当前没有可配置的打印机"
        />

        <div
          v-else-if="!selectedPrinter"
          class="rounded-xl border border-dashed border-default px-4 py-8 text-center text-sm text-muted"
        >
          先选择一台打印机，再查看或编辑 ACL 规则。
        </div>

        <div v-else-if="loadingPrinterACL" class="rounded-xl border border-default px-4 py-8 text-center text-sm text-muted">
          正在加载当前打印机的权限规则...
        </div>

        <div v-else class="space-y-4">
          <UAlert
            v-if="printerACLRules.length === 0"
            icon="i-lucide-info"
            color="primary"
            variant="soft"
            title="当前无规则，默认所有普通用户可用"
          />

          <div v-if="printerACLValidationErrors.length > 0" class="rounded-xl border border-error/30 bg-error/5 p-4">
            <div class="text-sm font-medium text-error">保存前请先修正以下问题：</div>
            <ul class="mt-2 space-y-1 text-sm text-error">
              <li v-for="error in printerACLValidationErrors" :key="error">{{ error }}</li>
            </ul>
          </div>

          <div class="space-y-3">
            <div
              v-for="(rule, index) in printerACLRules"
              :key="rule.clientKey"
              class="rounded-2xl border border-default bg-elevated/40 p-4 space-y-3"
            >
              <div class="flex items-center justify-between gap-3">
                <div class="text-sm font-medium">规则 {{ index + 1 }}</div>
                <UButton
                  size="sm"
                  variant="ghost"
                  color="error"
                  icon="i-lucide-trash-2"
                  :disabled="savingPrinterACL"
                  @click="removePrinterACLRule(index)"
                >
                  删除
                </UButton>
              </div>

              <div class="grid grid-cols-1 xl:grid-cols-[140px_minmax(0,1fr)_140px] gap-3">
                <div>
                  <label class="block text-xs text-muted mb-1">主体类型</label>
                  <USelect
                    :model-value="rule.subjectType"
                    :items="subjectTypeItems"
                    value-key="value"
                    label-key="label"
                    :disabled="savingPrinterACL"
                    @update:model-value="onPrinterACLSubjectTypeChange(rule, $event)"
                  />
                </div>

                <div v-if="rule.subjectType === 'role'">
                  <label class="block text-xs text-muted mb-1">角色</label>
                  <USelect
                    :model-value="rule.subjectRole"
                    :items="roleItems"
                    value-key="value"
                    label-key="label"
                    :disabled="savingPrinterACL"
                    @update:model-value="onPrinterACLRoleChange(rule, $event)"
                  />
                </div>

                <div v-else-if="rule.subjectType === 'user'">
                  <label class="block text-xs text-muted mb-1">用户</label>
                  <UInput
                    v-model="rule.subjectUserKeyword"
                    size="sm"
                    class="mb-2"
                    placeholder="按账号 / 联系人 / 邮箱 / 电话搜索用户"
                    :disabled="savingPrinterACL"
                  />
                  <USelect
                    :model-value="rule.subjectUserId"
                    :items="userItemsForRule(rule).items"
                    value-key="value"
                    label-key="label"
                    placeholder="选择用户"
                    :disabled="savingPrinterACL"
                    @update:model-value="onPrinterACLUserChange(rule, $event)"
                  />
                  <p class="mt-1 text-xs text-muted">
                    共 {{ userItemsForRule(rule).total }} 人，当前匹配 {{ userItemsForRule(rule).matched }} 人
                    <span v-if="userItemsForRule(rule).truncated">，仅展示前 {{ userItemsForRule(rule).limit }} 项</span>
                  </p>
                </div>

                <div v-else>
                  <label class="block text-xs text-muted mb-1">打印分组</label>
                  <UInput
                    v-model="rule.subjectGroupKeyword"
                    size="sm"
                    class="mb-2"
                    placeholder="按分组名 / 描述 / 成员搜索分组"
                    :disabled="savingPrinterACL"
                  />
                  <USelect
                    :model-value="rule.subjectGroupId"
                    :items="groupItemsForRule(rule).items"
                    value-key="value"
                    label-key="label"
                    placeholder="选择打印分组"
                    :disabled="savingPrinterACL"
                    @update:model-value="onPrinterACLGroupChange(rule, $event)"
                  />
                  <p class="mt-1 text-xs text-muted">
                    共 {{ groupItemsForRule(rule).total }} 组，当前匹配 {{ groupItemsForRule(rule).matched }} 组
                    <span v-if="groupItemsForRule(rule).truncated">，仅展示前 {{ groupItemsForRule(rule).limit }} 项</span>
                  </p>
                </div>

                <div>
                  <label class="block text-xs text-muted mb-1">效果</label>
                  <USelect
                    :model-value="rule.effect"
                    :items="effectItems"
                    value-key="value"
                    label-key="label"
                    :disabled="savingPrinterACL"
                    @update:model-value="onPrinterACLEffectChange(rule, $event)"
                  />
                </div>
              </div>

              <div class="text-xs text-muted">
                {{ describePrinterACLRuleTarget(rule, usersById, printerGroupsById) }} · {{ effectLabel(rule.effect) }}
              </div>
            </div>
          </div>

          <div class="flex flex-wrap gap-2">
            <UButton
              variant="outline"
              icon="i-lucide-plus"
              :disabled="savingPrinterACL"
              @click="addPrinterACLRule"
            >
              新增规则
            </UButton>
            <UButton
              color="primary"
              icon="i-lucide-save"
              :loading="savingPrinterACL"
              :disabled="savingPrinterACL || loadingPrinterACL || !selectedPrinter"
              @click="savePrinterACL"
            >
              保存规则
            </UButton>
          </div>
        </div>
      </div>
    </UCard>

    <UCard>
      <template #header>
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h2 class="text-xl font-bold flex items-center gap-2">
              <UIcon name="i-lucide-folder-tree" class="w-5 h-5" />
              打印分组
            </h2>
            <p class="text-sm text-muted mt-1">按业务部门、楼层、机型或人员范围维护可复用的打印分组。</p>
          </div>
          <UButton
            variant="outline"
            icon="i-lucide-refresh-cw"
            :loading="loadingPrinterGroups"
            :disabled="savingPrinterGroup"
            @click="loadPrinterGroups"
          >
            刷新分组
          </UButton>
        </div>
      </template>

      <div class="grid grid-cols-1 xl:grid-cols-[minmax(320px,380px)_minmax(0,1fr)] gap-4">
        <div class="rounded-2xl border border-default bg-elevated/30 p-4 space-y-3">
          <div class="text-sm font-medium">{{ isEditingPrinterGroup ? '编辑打印分组' : '新建打印分组' }}</div>
          <div>
            <label class="block text-sm font-medium mb-1">分组名称</label>
            <UInput
              v-model="printerGroupForm.name"
              placeholder="例如：财务部 / 前台 / A3 彩打"
              :disabled="savingPrinterGroup"
              :color="printerGroupFormErrors.name ? 'error' : undefined"
            />
            <p v-if="printerGroupFormErrors.name" class="text-xs text-error mt-1">{{ printerGroupFormErrors.name }}</p>
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">描述</label>
            <UTextarea
              v-model="printerGroupForm.description"
              :rows="3"
              placeholder="可选，说明该分组适用范围或维护备注"
              :disabled="savingPrinterGroup"
            />
          </div>
          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <label class="block text-sm font-medium">成员用户</label>
              <span class="text-xs text-muted">已选 {{ printerGroupForm.memberUserIds.length }} 人</span>
            </div>
            <UInput
              v-model="printerGroupMemberKeyword"
              size="sm"
              placeholder="按账号 / 联系人 / 邮箱 / 电话搜索成员用户"
              :disabled="savingPrinterGroup"
            />
            <p class="text-xs text-muted">
              共 {{ printerGroupMemberItems.total }} 人，当前匹配 {{ printerGroupMemberItems.matched }} 人
            </p>
            <div class="max-h-72 overflow-auto rounded-xl border border-default divide-y divide-default">
              <label
                v-for="option in printerGroupMemberItems.items"
                :key="option.id"
                class="flex cursor-pointer items-center justify-between gap-3 px-3 py-2 text-sm hover:bg-muted/30"
              >
                <span>{{ option.label }}</span>
                <UCheckbox
                  :model-value="printerGroupForm.memberUserIds.includes(option.id)"
                  :disabled="savingPrinterGroup"
                  @update:model-value="onPrinterGroupMemberToggle(option.id, !!$event)"
                />
              </label>
              <div v-if="printerGroupMemberItems.items.length === 0" class="px-3 py-4 text-sm text-muted">
                {{ printerGroupMemberItems.total === 0 ? '当前还没有可加入分组的用户。' : '当前没有匹配的用户，请调整搜索关键词。' }}
              </div>
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <UButton
              color="primary"
              icon="i-lucide-save"
              :loading="savingPrinterGroup"
              :disabled="savingPrinterGroup"
              @click="savePrinterGroup"
            >
              {{ isEditingPrinterGroup ? '保存分组' : '创建分组' }}
            </UButton>
            <UButton variant="ghost" :disabled="savingPrinterGroup" @click="resetPrinterGroupForm">重置</UButton>
          </div>
        </div>

        <div class="space-y-3">
          <UAlert
            v-if="!loadingPrinterGroups && printerGroups.length === 0"
            icon="i-lucide-info"
            color="primary"
            variant="soft"
            title="当前还没有打印分组"
            description="先创建分组，再在打印机 ACL 里把某台打印机授权给该分组。"
          />

          <div
            v-for="group in printerGroups"
            :key="group.id"
            class="rounded-2xl border border-default bg-elevated/30 p-4 space-y-3"
          >
            <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
              <div class="space-y-1">
                <div class="flex flex-wrap items-center gap-2">
                  <div class="font-medium">{{ group.name }}</div>
                  <UBadge color="neutral" variant="subtle" size="sm">ID: {{ group.id }}</UBadge>
                  <UBadge color="primary" variant="subtle" size="sm">{{ group.memberUserIds?.length || 0 }} 人</UBadge>
                </div>
                <p v-if="group.description" class="text-sm text-muted">{{ group.description }}</p>
                <p v-else class="text-sm text-muted">暂无分组描述</p>
              </div>
              <div class="flex flex-wrap gap-2">
                <UButton size="sm" variant="ghost" icon="i-lucide-pencil" @click="editPrinterGroup(group)">编辑</UButton>
                <UButton
                  size="sm"
                  variant="outline"
                  color="error"
                  icon="i-lucide-trash-2"
                  :loading="deletingPrinterGroupId === group.id"
                  :disabled="savingPrinterGroup || deletingPrinterGroupId === group.id"
                  @click="deletePrinterGroup(group)"
                >
                  删除
                </UButton>
              </div>
            </div>

            <div>
              <div class="mb-2 text-xs text-muted">成员列表</div>
              <div v-if="group.members?.length" class="flex flex-wrap gap-2">
                <UBadge
                  v-for="member in group.members"
                  :key="member.id"
                  color="primary"
                  variant="soft"
                  size="sm"
                >
                  {{ member.username }} · {{ member.role === 'admin' ? '管理员' : '普通用户' }}
                </UBadge>
              </div>
              <div v-else class="text-sm text-muted">当前分组还没有成员。</div>
            </div>
          </div>
        </div>
      </div>
    </UCard>

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
import {
  buildPrinterACLGroupItems,
  buildPrinterACLUpdatePayload,
  buildPrinterACLUserItems,
  buildPrinterGroupMemberItems,
  createDefaultPrinterACLRule,
  describePrinterACLRuleTarget,
  normalizePrinterACLRule,
  validatePrinterACLConfig
} from '../utils/printerAcl'

const toast = useToast()
const emit = defineEmits(['logout'])

const users = ref([])
const printers = ref([])
const printerGroups = ref([])
const form = ref(createEmptyUserForm())
const printFilters = ref({ username: '', start: '', end: '' })
const printRecords = ref([])
const settings = ref(createDefaultSettings())
const selectedPrinter = ref('')
const printerACLRules = ref([])
const printerGroupForm = ref(createEmptyPrinterGroupForm())
const printerGroupMemberKeyword = ref('')

const savingUser = ref(false)
const savingSettings = ref(false)
const syncingLDAP = ref(false)
const deletingUserId = ref(null)
const deletingPrinterGroupId = ref(null)
const pendingDeleteUser = ref(null)
const showDeleteModal = ref(false)
const formErrors = ref({})
const printerGroupFormErrors = ref({})
const loadingPrinters = ref(false)
const loadingPrinterGroups = ref(false)
const loadingPrinterACL = ref(false)
const savingPrinterACL = ref(false)
const savingPrinterGroup = ref(false)
const printerACLValidationErrors = ref([])

const isEditing = computed(() => !!form.value.id)
const isLDAPEditing = computed(() => isEditing.value && form.value.authSource === 'ldap')
const isEditingPrinterGroup = computed(() => !!printerGroupForm.value.id)
const usersById = computed(() => new Map(users.value.map((user) => [user.id, user])))
const printerGroupsById = computed(() => new Map(printerGroups.value.map((group) => [group.id, group])))
const printerByUri = computed(() => new Map(printers.value.map((printer) => [printer.uri, printer])))
const printerItems = computed(() =>
  printers.value.map((printer) => ({
    label: `${printer.name} — ${printer.uri}`,
    value: printer.uri
  }))
)
const printerGroupMemberItems = computed(() => buildPrinterGroupMemberItems(users.value, {
  keyword: printerGroupMemberKeyword.value,
  selectedUserIds: printerGroupForm.value.memberUserIds
}))
const printerGroupItems = computed(() =>
  printerGroups.value.map((group) => ({
    label: `${group.name}（${group.memberUserIds?.length || 0} 人）`,
    value: group.id
  }))
)
const selectedPrinterMeta = computed(() => printerByUri.value.get(selectedPrinter.value) || null)

const roleItems = [
  { label: '普通用户', value: 'user' },
  { label: '管理员', value: 'admin' }
]

const subjectTypeItems = [
  { label: '角色', value: 'role' },
  { label: '用户', value: 'user' },
  { label: '分组', value: 'group' }
]

const effectItems = [
  { label: '允许（allow）', value: 'allow' },
  { label: '拒绝（deny）', value: 'deny' }
]

let nextPrinterACLRuleKey = 1
let printerACLRequestToken = 0

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

function createEmptyPrinterGroupForm() {
  return {
    id: null,
    name: '',
    description: '',
    memberUserIds: []
  }
}

function normalizeMemberUserIds(values) {
  return Array.from(new Set((Array.isArray(values) ? values : [])
    .map((value) => Number(value))
    .filter((value) => Number.isInteger(value) && value > 0)))
    .sort((a, b) => a - b)
}

function createEditablePrinterGroup(group = {}) {
  return {
    id: group.id ?? null,
    name: String(group.name || ''),
    description: String(group.description || ''),
    memberUserIds: normalizeMemberUserIds(group.memberUserIds)
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

function mapLDAPSyncStatus(data) {
  return {
    lastStartedAt: data?.ldapSyncStatus?.lastStartedAt || '',
    lastFinishedAt: data?.ldapSyncStatus?.lastFinishedAt || '',
    lastStatus: data?.ldapSyncStatus?.lastStatus || '',
    lastMessage: data?.ldapSyncStatus?.lastMessage || '',
    lastCount: data?.ldapSyncStatus?.lastCount || 0
  }
}

function authSourceLabel(source) {
  return source === 'ldap' ? 'LDAP' : '本地'
}

function authSourceColor(source) {
  return source === 'ldap' ? 'primary' : 'neutral'
}

function effectLabel(effect) {
  return effect === 'deny' ? '拒绝（deny）' : '允许（allow）'
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

function makeEditablePrinterACLRule(rule = createDefaultPrinterACLRule()) {
  return {
    ...normalizePrinterACLRule(rule),
    subjectUserKeyword: '',
    subjectGroupKeyword: '',
    clientKey: `printer-acl-rule-${nextPrinterACLRuleKey++}`
  }
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

function resetPrinterGroupForm() {
  printerGroupForm.value = createEmptyPrinterGroupForm()
  printerGroupMemberKeyword.value = ''
  printerGroupFormErrors.value = {}
}

function editPrinterGroup(group) {
  printerGroupForm.value = createEditablePrinterGroup(group)
  printerGroupMemberKeyword.value = ''
  printerGroupFormErrors.value = {}
}

function validatePrinterGroupForm() {
  const errors = {}
  if (!String(printerGroupForm.value.name || '').trim()) {
    errors.name = '分组名称不能为空'
  }
  printerGroupFormErrors.value = errors
  return Object.keys(errors).length === 0
}

function onPrinterGroupMemberToggle(userId, checked) {
  const current = new Set(normalizeMemberUserIds(printerGroupForm.value.memberUserIds))
  if (checked) {
    current.add(userId)
  } else {
    current.delete(userId)
  }
  printerGroupForm.value.memberUserIds = Array.from(current).sort((a, b) => a - b)
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

function userItemsForRule(rule) {
  return buildPrinterACLUserItems(users.value, {
    keyword: rule.subjectUserKeyword,
    selectedUserId: rule.subjectUserId
  })
}

function groupItemsForRule(rule) {
  return buildPrinterACLGroupItems(printerGroups.value, {
    keyword: rule.subjectGroupKeyword,
    selectedGroupId: rule.subjectGroupId
  })
}

async function loadPrinterGroups() {
  loadingPrinterGroups.value = true
  try {
    printerGroups.value = await apiGetJSON('/api/admin/printer-groups', handleUnauthorized) || []
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载分组失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    loadingPrinterGroups.value = false
  }
}

async function loadPrinters() {
  loadingPrinters.value = true
  try {
    printers.value = await apiGetJSON('/api/printers', handleUnauthorized) || []
    if (selectedPrinter.value && !printerByUri.value.has(selectedPrinter.value)) {
      selectedPrinter.value = ''
      printerACLRules.value = []
      printerACLValidationErrors.value = []
    }
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载打印机失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    loadingPrinters.value = false
  }
}

async function loadPrinterACL(printerURI) {
  const targetPrinter = String(printerURI || '').trim()
  printerACLRequestToken += 1
  const requestToken = printerACLRequestToken

  printerACLRules.value = []
  printerACLValidationErrors.value = []

  if (!targetPrinter) {
    loadingPrinterACL.value = false
    return
  }

  loadingPrinterACL.value = true
  try {
    const data = await apiGetJSON(`/api/admin/printer-acl?printer=${encodeURIComponent(targetPrinter)}`, handleUnauthorized)
    if (requestToken !== printerACLRequestToken || selectedPrinter.value !== targetPrinter) {
      return
    }
    const rules = Array.isArray(data?.rules) ? data.rules : []
    printerACLRules.value = rules.map((rule) => makeEditablePrinterACLRule(rule))
  } catch (error) {
    if (requestToken !== printerACLRequestToken) {
      return
    }
    printerACLRules.value = []
    if (error.status !== 401) {
      toast.add({ title: '加载权限失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    if (requestToken === printerACLRequestToken) {
      loadingPrinterACL.value = false
    }
  }
}

function onPrinterSelectionChange(value) {
  selectedPrinter.value = value || ''
  loadPrinterACL(selectedPrinter.value)
}

function addPrinterACLRule() {
  printerACLRules.value.push(makeEditablePrinterACLRule())
}

async function savePrinterGroup() {
  if (!validatePrinterGroupForm()) {
    return
  }

  savingPrinterGroup.value = true
  try {
    const payload = {
      name: String(printerGroupForm.value.name || '').trim(),
      description: String(printerGroupForm.value.description || '').trim(),
      memberUserIds: normalizeMemberUserIds(printerGroupForm.value.memberUserIds)
    }
    const isEditingGroup = !!printerGroupForm.value.id
    const url = isEditingGroup ? `/api/admin/printer-groups/${printerGroupForm.value.id}` : '/api/admin/printer-groups'
    const method = isEditingGroup ? 'PUT' : 'POST'
    await apiSendJSON(url, method, payload, handleUnauthorized)
    toast.add({
      title: isEditingGroup ? '分组已更新' : '分组已创建',
      description: `打印分组 ${payload.name} 已保存`,
      color: 'success',
      icon: 'i-lucide-check-circle'
    })
    await loadPrinterGroups()
    resetPrinterGroupForm()
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '保存分组失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    savingPrinterGroup.value = false
  }
}

async function deletePrinterGroup(group) {
  deletingPrinterGroupId.value = group.id
  try {
    await apiDelete(`/api/admin/printer-groups/${group.id}`, handleUnauthorized)
    toast.add({
      title: '分组已删除',
      description: `打印分组 ${group.name} 已删除`,
      color: 'success',
      icon: 'i-lucide-check-circle'
    })
    await loadPrinterGroups()
    if (printerGroupForm.value.id === group.id) {
      resetPrinterGroupForm()
    }
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '删除分组失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    deletingPrinterGroupId.value = null
  }
}

function onPrinterACLSubjectTypeChange(rule, value) {
  rule.subjectType = value === 'user' ? 'user' : value === 'group' ? 'group' : 'role'
  if (rule.subjectType === 'role') {
    rule.subjectRole = rule.subjectRole || 'user'
    rule.subjectUserId = null
    rule.subjectGroupId = null
  } else if (rule.subjectType === 'user') {
    rule.subjectRole = ''
    rule.subjectUserId = rule.subjectUserId ?? null
    rule.subjectGroupId = null
  } else {
    rule.subjectRole = ''
    rule.subjectUserId = null
    rule.subjectGroupId = rule.subjectGroupId ?? null
  }
  printerACLValidationErrors.value = []
}

function onPrinterACLUserChange(rule, value) {
  if (value == null || value === '') {
    rule.subjectUserId = null
  } else {
    const parsed = Number(value)
    rule.subjectUserId = Number.isInteger(parsed) ? parsed : null
  }
  printerACLValidationErrors.value = []
}

function onPrinterACLGroupChange(rule, value) {
  if (value == null || value === '') {
    rule.subjectGroupId = null
  } else {
    const parsed = Number(value)
    rule.subjectGroupId = Number.isInteger(parsed) ? parsed : null
  }
  printerACLValidationErrors.value = []
}

function onPrinterACLRoleChange(rule, value) {
  rule.subjectRole = value || ''
  printerACLValidationErrors.value = []
}

function onPrinterACLEffectChange(rule, value) {
  rule.effect = value || ''
  printerACLValidationErrors.value = []
}

async function refreshPrinterACLSection() {
  await Promise.all([loadPrinters(), loadPrinterGroups()])
  if (selectedPrinter.value) {
    await loadPrinterACL(selectedPrinter.value)
  }
}

async function savePrinterACL() {
  const targetPrinter = String(selectedPrinter.value || '').trim()
  const validation = validatePrinterACLConfig(targetPrinter, printerACLRules.value)
  printerACLValidationErrors.value = validation.errors
  if (!validation.ok) {
    toast.add({
      title: '规则未保存',
      description: validation.errors[0],
      color: 'warning',
      icon: 'i-lucide-triangle-alert'
    })
    return
  }

  const requestToken = ++printerACLRequestToken
  const targetPrinterName = selectedPrinterMeta.value?.name || '当前打印机'

  savingPrinterACL.value = true
  try {
    const payload = buildPrinterACLUpdatePayload(printerACLRules.value)
    const data = await apiSendJSON(`/api/admin/printer-acl?printer=${encodeURIComponent(targetPrinter)}`, 'PUT', payload, handleUnauthorized)
    if (requestToken !== printerACLRequestToken || selectedPrinter.value !== targetPrinter) {
      return
    }
    const rules = Array.isArray(data?.rules) ? data.rules : []
    printerACLRules.value = rules.map((rule) => makeEditablePrinterACLRule(rule))
    printerACLValidationErrors.value = []
    toast.add({
      title: '保存成功',
      description: `已更新 ${targetPrinterName} 的权限规则`,
      color: 'success',
      icon: 'i-lucide-check-circle'
    })
  } catch (error) {
    if (error.status !== 401) {
      toast.add({
        title: '保存失败',
        description: error?.message || '权限规则格式非法',
        color: 'error',
        icon: 'i-lucide-x-circle'
      })
    }
  } finally {
    if (requestToken === printerACLRequestToken) {
      savingPrinterACL.value = false
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
      ldapSyncStatus: mapLDAPSyncStatus(data)
    }
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '加载失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  }
}

async function loadLDAPSyncStatus() {
  try {
    const data = await apiGetJSON('/api/admin/settings', handleUnauthorized)
    settings.value.ldapSyncStatus = mapLDAPSyncStatus(data)
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
  let syncSucceeded = false
  try {
    const data = await apiSendJSON('/api/admin/ldap/sync', 'POST', {}, handleUnauthorized)
    const report = data?.report || {}
    syncSucceeded = true
    toast.add({
      title: '同步成功',
      description: `已同步 upserted=${report.upserted || 0} skipped=${report.skipped || 0} missingMarked=${report.missingMarked || 0}`,
      color: 'success',
      icon: 'i-lucide-check-circle'
    })
  } catch (error) {
    if (error.status !== 401) {
      toast.add({ title: '同步失败', description: error.message, color: 'error', icon: 'i-lucide-x-circle' })
    }
  } finally {
    syncingLDAP.value = false
  }

  if (syncSucceeded) {
    await Promise.allSettled([loadUsers(), loadSettings()])
  } else {
    await loadLDAPSyncStatus()
  }
}

onMounted(async () => {
  await Promise.all([loadUsers(), loadPrintRecords(), loadSettings(), loadPrinters(), loadPrinterGroups()])
})
</script>
