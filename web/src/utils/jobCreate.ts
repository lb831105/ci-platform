import { applyJobTypePreset, findJobType } from '../types/jobType.ts'
import type { PipelinePayload } from '../types/pipeline.ts'

// 首次创建向导的纯函数：把最小输入补成完整任务配置。
// 单独抽出来便于单测，也避免把校验逻辑散落在组件里。
export interface QuickCreateOptions {
  name: string
  description?: string
  environment?: string
  gitUrl?: string
  gitBranch?: string
  defaultBranch?: string
}

export function buildQuickCreatePayload(typeId: string, options: QuickCreateOptions): PipelinePayload {
  const name = options.name.trim()
  if (!name) {
    throw new Error('请输入任务名称')
  }
  const type = findJobType(typeId)
  if (!type) {
    throw new Error('请选择一个受支持的任务类型')
  }
  const gitUrl = (options.gitUrl ?? '').trim()
  // 项目模板的步骤依赖源码：没有仓库地址时构建大概率失败，
  // 在创建前就拦住，避免用户第一次构建就得到一堆“文件不存在”。
  if (type.id !== 'freestyle' && !gitUrl) {
    throw new Error('该模板需要填写 Git 仓库地址；没有代码仓库请选择自由风格')
  }
  const preset = applyJobTypePreset(type.id)
  return {
    ...preset,
    name,
    description: (options.description ?? '').trim(),
    environment: (options.environment ?? '').trim(),
    git_url: gitUrl,
    git_branch: (options.gitBranch ?? '').trim() || (options.defaultBranch ?? '').trim() || 'main',
  }
}
