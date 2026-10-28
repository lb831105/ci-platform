// 首次创建向导的模板补全与校验（纯函数，无 DOM 依赖）。
import { buildQuickCreatePayload } from '../src/utils/jobCreate.ts'

function assert(cond: unknown, label: string): void {
  if (!cond) {
    console.log('FAIL ' + label)
    process.exitCode = 1
  } else {
    console.log('PASS ' + label)
  }
}

const maven = buildQuickCreatePayload('maven', {
  name: ' order-service ',
  gitUrl: ' https://git.example.com/order.git ',
  gitBranch: ' release/1.0 ',
  defaultBranch: 'main',
  environment: ' production ',
})
assert(maven.name === 'order-service', '任务名 trim')
assert(maven.git_url === 'https://git.example.com/order.git', 'Git URL trim')
assert(maven.git_branch === 'release/1.0', '分支保留')
assert(maven.environment === 'production', '环境 trim')
assert((maven.steps ?? []).length > 0, '模板步骤被带入')
assert(maven.artifacts?.enabled === true, '模板产物配置被带入')

const freestyle = buildQuickCreatePayload('freestyle', { name: 'script', defaultBranch: 'develop' })
assert(freestyle.git_url === '', '自由风格允许无仓库')
assert(freestyle.git_branch === 'develop', '未填分支时使用默认分支')

for (const [type, opts, message] of [
  ['maven', { name: 'x' }, '项目模板缺 Git 仓库被拒'],
  ['maven', { name: '' }, '空任务名被拒'],
  ['multibranch', { name: 'x', gitUrl: 'https://git/x' }, '不支持类型被拒'],
] as const) {
  let rejected = false
  try {
    buildQuickCreatePayload(type, opts)
  } catch {
    rejected = true
  }
  assert(rejected, message)
}
