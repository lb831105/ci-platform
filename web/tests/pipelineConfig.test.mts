// 纯函数单测（无框架）：node --experimental-strip-types tests/pipelineConfig.test.mts
//
// 回归目标：服务端 SSH 发布字段带 `omitempty`（只用 server_id 时 JSON 里没有
// host/username/password），前端若直接 .trim() 就会在「绑定服务器」的任务上
// 保存时报 `Cannot read properties of undefined (reading 'trim')`。
import { normalizeSshPublish, sshPublishIsBlank } from '../src/utils/pipelineConfig.ts'

function assert(cond: unknown, label: string): void {
  if (!cond) {
    console.log('FAIL ' + label)
    process.exitCode = 1
  } else {
    console.log('PASS ' + label)
  }
}

// 导入的 Jenkins 任务：只带 server_id，服务端 omitempty 导致 host/username 等键缺失。
const imported = {
  name: '',
  enabled: true,
  server_id: 'srv-test',
  remote_base_dir: 'admin_dnysj',
  source_files: ['dist.tar.gz'],
  remove_prefix: '',
  exec_command: 'tar zxf dist.tar.gz',
  exec_timeout_sec: 120,
}

// 1) 旧写法（直接 .trim()）确实会抛异常——这就是用户看到的报错根因
let crashed = false
try {
  const broken = imported as Record<string, unknown>
  ;(broken.host as string).trim()
} catch (e) {
  crashed = e instanceof TypeError && String(e.message).includes("reading 'trim'")
}
assert(crashed, '未归一化时 .trim() 抛 TypeError（复现原报错）')

// 2) 归一化后所有字符串字段都存在
const normalized = normalizeSshPublish(imported, 0)
assert(normalized.host === '' && normalized.username === '', 'host/username 补齐为空串')
assert(normalized.auth_type === 'key' && normalized.port === 22, 'auth_type/port 有默认值')
assert(normalized.name === '发布 1', '空名称自动命名')
assert(normalized.password === '', '密码不回显')
assert(normalized.exec_timeout_sec === 120 && normalized.clean_remote === false, 'transfer 选项默认值')
let trims = 0
for (const value of [normalized.host, normalized.username, normalized.remote_base_dir, normalized.private_key_path, normalized.exec_command]) {
  value.trim()
  trims++
}
assert(trims === 5, '归一化后所有字段都能 .trim()')

// 3) 有 server_id 的目标不算空白，保存时必须保留
assert(!sshPublishIsBlank(normalized), '有 server_id 的目标不被当成空行丢弃')

// 4) 完全空白的行才丢弃；「关闭但有内容」的保留（与 Jenkins 一致）
const blank = normalizeSshPublish({}, 1)
assert(sshPublishIsBlank(blank), '全新空行判定为空白')
const disabledButFilled = normalizeSshPublish({ enabled: false, host: '1.2.3.4' }, 2)
assert(!sshPublishIsBlank(disabledButFilled), '关闭但填了内容的目标保留')
