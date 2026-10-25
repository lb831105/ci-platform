import type { PipelinePayload } from '@/types/pipeline'

export interface ValidateOptions {
  // 编辑任务时后端已保存密码且前端不回显，允许密码为空（服务端会沿用旧值）。
  keepSshPassword?: boolean
}

/**
 * 表单校验，返回错误信息数组，空数组代表通过。
 * 与后端 pipeline_service.go 的校验规则保持一致，前端提前拦截以获得即时反馈。
 */
export function validatePipeline(payload: PipelinePayload, options: ValidateOptions = {}): string[] {
  const errors: string[] = []

  if (payload.name.trim() === '') {
    errors.push('任务名称不能为空')
  }
  if (payload.steps.length === 0) {
    errors.push('至少需要一个构建步骤')
  }
  payload.steps.forEach((step, index) => {
    if (step.run.trim() === '') {
      errors.push(`第 ${index + 1} 个步骤的命令不能为空`)
    }
  })

  const artifacts = payload.artifacts
  if (artifacts?.enabled) {
    const files = artifacts.files.map((file) => file.trim()).filter(Boolean)
    if (files.length === 0) {
      errors.push('启用产物归档时至少需要一个匹配模式')
    }
    for (const file of files) {
      if (
        file.startsWith('/') ||
        /^[A-Za-z]:[\\/]/.test(file) ||
        file.split(/[\\/]/).includes('..')
      ) {
        errors.push(`产物匹配模式必须是工作区内的相对路径：${file}`)
      }
    }
  }

  // 服务端字段带 omitempty（如只用 server_id 时没有 host/username/password），
  // 读取 JSON 后可能是 undefined，这里统一按空串处理，避免 .trim() 抛异常。
  const ssh = payload.ssh_publish
  if (ssh && ssh.enabled) {
    const serverID = (ssh.server_id ?? '').trim()
    const host = (ssh.host ?? '').trim()
    const username = (ssh.username ?? '').trim()
    const remoteBaseDir = (ssh.remote_base_dir ?? '').trim()
    const privateKeyPath = (ssh.private_key_path ?? '').trim()
    const password = (ssh.password ?? '').trim()
    if (!serverID) {
      // 旧版内联配置：要求主机/账号等字段。
      if (host === '') {
        errors.push('请选择系统配置中的 SSH 服务器（或填写旧版主机地址）')
      }
      if (username === '') {
        errors.push('SSH 用户名不能为空')
      }
      if (remoteBaseDir === '') {
        errors.push('SSH 远程基础目录不能为空')
      }
      if (ssh.auth_type === 'key' && privateKeyPath === '') {
        errors.push('使用密钥认证时必须填写私钥路径')
      }
      if (ssh.auth_type === 'password' && !options.keepSshPassword && password === '') {
        errors.push('使用密码认证时必须填写密码')
      }
    }
    if ((ssh.source_files ?? []).length === 0) {
      errors.push('SSH 待上传文件至少要配置一个匹配模式')
    }
  }

  return errors
}
