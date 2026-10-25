// downloadText 触发浏览器下载：用于导出任务配置（config.json 文档）。
// 用 Blob + 临时 <a> 而不是直接开新窗口，避免页面被下载内容替换掉。
export function downloadText(filename: string, text: string, mime = 'application/json;charset=utf-8'): void {
  const blob = new Blob([text], { type: mime })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  // 立即释放会让部分浏览器来不及下载，交给下一轮事件循环。
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
