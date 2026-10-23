// 新建任务时的「任务类型」选项，语义对齐 Jenkins New Item 页面。
//
// 平台底层只有一种统一的 Pipeline 模型（步骤 + Git + SSH），
// 构建直接在服务器本地执行，使用已安装的 Go / Maven / Node / JDK。
// 这里的类型只负责为配置表单提供预设；不支持的 Jenkins 原生类型
// 明确置灰并标注原因，而不是伪装成可用。

import { emptyPipeline, type PipelinePayload } from './pipeline'

export interface JobTypePreset {
  id: string
  title: string
  description: string
  badge: string
  color: string
  enabled: boolean
  disabledReason?: string
  preset?: Partial<PipelinePayload>
}

export const JOB_TYPES: JobTypePreset[] = [
  {
    id: 'freestyle',
    title: '构建一个自由风格的软件项目',
    description: '最通用：编排构建步骤，按需拉取代码并 SSH 发布。命令直接使用服务器本地环境。',
    badge: 'F',
    color: '#2563eb',
    enabled: true,
    preset: {},
  },
  {
    id: 'golang',
    title: '构建一个 Golang 项目',
    description: '使用服务器上的 Go 工具链编译为可执行文件并测试；产物默认名为 app（Windows 可改为 app.exe）。',
    badge: 'G',
    color: '#00add8',
    enabled: true,
    preset: {
      steps: [
        { name: 'build', run: 'go build -o app .' },
        { name: 'test', run: 'go test ./...' },
      ],
      artifacts: { enabled: true, files: ['app', 'app.exe'] },
    },
  },
  {
    id: 'nodejs',
    title: '构建一个 NodeJS 项目',
    description: '使用服务器上的 Node.js 与 npm 安装依赖并构建，适合前端与 Node 服务。',
    badge: 'N',
    color: '#43853d',
    enabled: true,
    preset: {
      steps: [
        { name: 'install', run: 'npm ci || npm install' },
        { name: 'build', run: 'npm run build' },
      ],
      // 常见前端输出目录；默认不启用，避免项目输出到 build/ 等目录时误报。
      artifacts: { enabled: false, files: ['dist/**'] },
    },
  },
  {
    id: 'maven',
    title: '构建一个 Maven 项目',
    description: '使用服务器上的 Maven 打包 jar，适合 Java / Spring Boot 项目。',
    badge: 'M',
    color: '#c71a36',
    enabled: true,
    preset: {
      steps: [{ name: 'package', run: 'mvn -B -DskipTests clean package' }],
      artifacts: { enabled: true, files: ['target/*.jar'] },
    },
  },
  {
    id: 'pipeline',
    title: '流水线',
    description: '以多个串行步骤编排构建阶段，等价于在 Jenkinsfile 中定义 stage。',
    badge: 'P',
    color: '#3f51b5',
    enabled: true,
    preset: {
      steps: [
        { name: 'build', run: '' },
        { name: 'test', run: '' },
      ],
    },
  },
  {
    id: 'multibranch',
    title: '多分支流水线',
    description: '按分支自动发现并触发构建。',
    badge: 'B',
    color: '#009688',
    enabled: false,
    disabledReason: '基础版暂不支持',
  },
  {
    id: 'multiconfig',
    title: '多配置项目',
    description: '用参数矩阵一次生成多份构建配置。',
    badge: 'X',
    color: '#607d8b',
    enabled: false,
    disabledReason: '基础版暂不支持',
  },
  {
    id: 'folder',
    title: '文件夹',
    description: '用于把多个任务分组管理。',
    badge: 'D',
    color: '#795548',
    enabled: false,
    disabledReason: '基础版暂不支持',
  },
]

// findJobType 只返回受支持（enabled）的类型，避免把置灰项带进表单。
export function findJobType(id: string | null | undefined): JobTypePreset | undefined {
  if (!id) {
    return undefined
  }
  return JOB_TYPES.find((type) => type.id === id && type.enabled)
}

// applyJobTypePreset 基于预设生成一份完整表单，未知类型回退为自由风格。
export function applyJobTypePreset(id: string | null | undefined): PipelinePayload {
  const type = findJobType(id)
  if (!type?.preset) {
    return emptyPipeline()
  }
  return { ...emptyPipeline(), ...type.preset }
}
