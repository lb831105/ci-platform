// 个人资料（与 Go model.Profile 对齐）：当前登录用户的联系方式与邮箱验证状态。
export interface Profile {
  name: string
  email?: string
  wecom_id?: string
  phone?: string
  email_verified: boolean
  // 内置管理员（系统配置里的登录用户）：联系方式只读，不支持邮箱验证。
  builtin: boolean
}
