// 任务配置的纯函数工具：加载配置时的归一化、保存前的空白判断。
// 与 Vue 无关，便于用最小脚本验证（服务端字段带 omitempty，JSON 里可能缺字段）。
import type { SshPublishConfig } from '@/types/pipeline'

/**
 * normalizeSshPublish 把服务端返回的发布目标补齐成「字段一定存在」的对象。
 *
 * 为什么必须做：Go 侧 host/username/auth_type/private_key_path/password 都是
 * `omitempty`（只用 server_id 时这些键根本不出现在 JSON 里）。若前端直接对这些
 * 字段调用 .trim()，就会在「导入的 Jenkins 任务」上抛
 * `Cannot read properties of undefined (reading 'trim')`。
 */
export function normalizeSshPublish(item: Partial<SshPublishConfig>, index: number): SshPublishConfig {
  const name = (item.name ?? '').trim()
  return {
    name: name !== '' ? name : `发布 ${index + 1}`,
    enabled: item.enabled ?? false,
    server_id: item.server_id ?? '',
    host: item.host ?? '',
    port: item.port ?? 22,
    username: item.username ?? '',
    auth_type: item.auth_type ?? 'key',
    private_key_path: item.private_key_path ?? '',
    // 密码永不回显：服务端也会清空，表单里保持空串等用户重填（保存时沿用旧值）。
    password: '',
    remote_base_dir: item.remote_base_dir ?? '',
    source_files: [...(item.source_files ?? [])],
    remove_prefix: item.remove_prefix ?? '',
    exec_command: item.exec_command ?? '',
    clean_remote: item.clean_remote ?? false,
    flatten: item.flatten ?? false,
    exec_timeout_sec: item.exec_timeout_sec ?? 0,
  }
}

/**
 * sshPublishIsBlank 判断一个发布目标是否「完全空白」，用于保存时丢弃空行。
 * 关闭的（enabled=false）但有内容的配置必须保留——与 Jenkins 一致。
 */
export function sshPublishIsBlank(item: SshPublishConfig): boolean {
  return !(
    item.enabled ||
    (item.server_id ?? '') !== '' ||
    (item.host ?? '').trim() !== '' ||
    (item.username ?? '').trim() !== '' ||
    (item.remote_base_dir ?? '').trim() !== '' ||
    (item.private_key_path ?? '').trim() !== '' ||
    (item.source_files ?? []).length > 0 ||
    (item.exec_command ?? '').trim() !== ''
  )
}
