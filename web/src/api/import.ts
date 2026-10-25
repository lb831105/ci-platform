import { http } from './request'
import type {
  JenkinsImportOptions,
  JenkinsImportResult,
  PlatformImportOptions,
  PlatformImportResult,
} from '@/types/import'

export const importApi = {
  // 从 Jenkins 的 config.xml 导入任务：服务端做 XML → 平台 JSON 的语义转换。
  // 用原始 XML 作为请求体（不是 JSON），任务名等参数走查询串。
  jenkins(xml: string, options: JenkinsImportOptions): Promise<JenkinsImportResult> {
    const query = new URLSearchParams({
      name: options.name,
      mode: options.mode,
      dry_run: options.dryRun ? 'true' : 'false',
      keep_source: options.keepSource ? 'true' : 'false',
    })
    return http.postRaw<JenkinsImportResult>(
      `/system/import/jenkins?${query.toString()}`,
      xml,
      'application/xml',
    )
  },

  // 导入平台自有 JSON 配置（导出文档或原生 config.json）。
  config(doc: string, options: PlatformImportOptions): Promise<PlatformImportResult> {
    const query = new URLSearchParams({
      name: options.name,
      mode: options.mode,
      dry_run: options.dryRun ? 'true' : 'false',
    })
    return http.postRaw<PlatformImportResult>(
      `/system/import/config?${query.toString()}`,
      doc,
      'application/json',
    )
  },
}