// 任务角色 Pattern 与「勾选任务」之间的互转。
//
// 为什么需要：Pattern 是正则，普通管理员更想「勾选 as_doc_admin、gomysqldump 这两个任务」，
// 而不是自己写 `as_doc_admin|gomysqldump`（`.`、`+`、`(` 这类字符还得转义，写错就静默不生效）。
// 后端仍按正则匹配（Jenkins Role Strategy 语义不变），这里只负责把勾选结果翻译成正则。

// escapeRegExp 转义正则元字符，让任务名按字面匹配。
export function escapeRegExp(name: string): string {
  return name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// patternFromJobs 由勾选的任务名生成 Pattern：`a|b`（后端会加 (?i)^(?: ... )$ 锚定）。
export function patternFromJobs(names: string[]): string {
  const uniq = [...new Set(names.map((n) => n.trim()).filter((n) => n !== ''))]
  uniq.sort((a, b) => a.localeCompare(b))
  return uniq.map(escapeRegExp).join('|')
}

// jobsFromPattern 从 Pattern 反推勾选状态：
// 只认「整体就是若干个被转义任务名的并集」这种由本页生成的形式，
// 手写复杂正则（含量词、分组）时返回空数组，页面就显示为「手写正则」。
export function jobsFromPattern(pattern: string, jobNames: string[]): string[] {
  const raw = (pattern ?? '').trim()
  if (!raw) {
    return []
  }
  const alternatives = raw.split('|').map((p) => p.trim())
  const picked: string[] = []
  for (const name of jobNames) {
    const escaped = escapeRegExp(name)
    if (alternatives.some((alt) => alt === escaped || alt === name)) {
      picked.push(name)
    }
  }
  return picked
}
