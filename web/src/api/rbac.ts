import { http } from './request'
import type { RbacAssignment, RbacData, RbacEffective, RbacRole, UserAccount } from '@/types/rbac'

// 「Manage and Assign Roles」接口：仅 Administer 可调用。
export const rbacApi = {
  get(): Promise<RbacData> {
    return http.get<RbacData>('/security/rbac')
  },

  // 生效预览：每个主体能看哪些任务、每个角色覆盖哪些任务（后端权威计算）。
  effective(): Promise<RbacEffective> {
    return http.get<RbacEffective>('/security/rbac/effective')
  },

  saveRoles(roles: RbacRole[]): Promise<RbacData> {
    return http.put<RbacData>('/security/rbac/roles', { roles })
  },

  saveAssignments(assignments: RbacAssignment[]): Promise<RbacData> {
    return http.put<RbacData>('/security/rbac/assignments', { assignments })
  },

  // 用户管理页数据源：内置管理员 + 平台用户 + 联系方式 + 角色摘要。
  users(): Promise<UserAccount[]> {
    return http.get<UserAccount[]>('/security/users')
  },

  // 邮箱必填：构建通知按人员邮箱发信；姓名必填且只允许汉字（用于界面展示）。
  addUser(name: string, password: string, email: string, displayName: string): Promise<RbacData> {
    return http.post<RbacData>('/security/users', {
      name,
      password,
      email,
      display_name: displayName,
    })
  },

  deleteUser(name: string): Promise<RbacData> {
    return http.post<RbacData>('/security/users/delete', { name })
  },

  resetUserPassword(name: string, password: string): Promise<void> {
    return http.put<void>('/security/users/password', { name, password })
  },

  // 设置用户姓名与联系方式（任务通知按用户名引用时解析邮箱与 @ 提醒）。
  setUserContact(payload: {
    name: string
    display_name: string
    email: string
    wecom_id: string
    phone: string
  }): Promise<RbacData> {
    return http.put<RbacData>('/security/users/contact', payload)
  },
}
