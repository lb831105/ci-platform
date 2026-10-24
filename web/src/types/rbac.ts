// RBAC：对齐 Jenkins Role Strategy（Manage and Assign Roles）。
export type RbacRoleKind = 'global' | 'item'

export interface RbacRole {
  name: string
  kind: RbacRoleKind
  pattern?: string // 任务角色：任务名匹配正则（忽略大小写、完全匹配）
  template?: string // 引用的模板角色（无 Pattern 的任务角色），权限取并集
  permissions: string[]
}

export interface RbacAssignment {
  role: string
  kind: RbacRoleKind
  subject: string // 用户名 / anonymous / authenticated
}

export interface RbacUserInfo {
  name: string
  created_at?: string
  // 通知联系方式（任务通知按用户名引用人员时解析）
  email?: string
  wecom_id?: string
  phone?: string
}

// 用户管理页（系统管理 → 用户管理）的账号模型。
export interface UserRoleRef {
  role: string
  kind: RbacRoleKind
  pattern?: string
}

export interface UserAccount {
  name: string
  email?: string
  wecom_id?: string
  phone?: string
  created_at?: string // 内置管理员没有创建时间
  // 内置管理员（系统配置里的登录用户）：不可删除、不可重置密码。
  builtin: boolean
  roles: UserRoleRef[]
}

export interface RbacPermissionItem {
  id: string
  title: string
}

export interface RbacPermissionGroup {
  id: string
  title: string
  hint?: string
  supported: boolean
  permissions?: RbacPermissionItem[]
}

export interface RbacData {
  roles: RbacRole[]
  assignments: RbacAssignment[]
  users: RbacUserInfo[]
  permission_groups: RbacPermissionGroup[]
}
