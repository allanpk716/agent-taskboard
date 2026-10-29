/** 「粘贴链接」的识别规则。保持纯函数，便于在 e2e 里直接覆盖各种粘贴文本。 */
export type PasteLinkResult =
  | { kind: 'path'; path: string }
  | { kind: 'guest-link' }
  | { kind: 'none' }

const noMatch: PasteLinkResult = { kind: 'none' }
const guestLink: PasteLinkResult = { kind: 'guest-link' }

// 只抓应用内路由。查询参数和 hash 一起抓；空格、成对符号和中英文标点会结束链接。
const routePattern = /\/(?:t|drafts|g)\/[A-Za-z0-9_-]+(?:[?#][^\s"',;:!?<>`（）()【】[\]{}「」『』“”‘’。，；：！？]*)?/g
const ticketNumberPattern = /\d{8}-\d{3,}/g
const sequencePattern = /#(\d+)/g
const trailingUrlPunctuation = /[.,;:!?…。，；：！？）)】\]」』”’»>]+$/
const urlBoundary = /[\s,;!?<("“‘「【（，；！？。：]/

type Candidate = { index: number; result: PasteLinkResult }

/** 在一段粘贴文本里找第一个能识别的站内地址、工单编号或序号。 */
export function parsePasteLink(text: string): PasteLinkResult {
  const candidates: Candidate[] = []

  for (const match of text.matchAll(routePattern)) {
    const result = routeResult(match[0])
    if (result && pathIsUrlPath(text, match.index)) candidates.push({ index: match.index, result })
  }
  for (const match of text.matchAll(ticketNumberPattern)) {
    candidates.push({ index: match.index, result: { kind: 'path', path: `/t/${match[0]}` } })
  }
  for (const match of text.matchAll(sequencePattern)) {
    candidates.push({ index: match.index, result: { kind: 'path', path: `/t/${match[1]}` } })
  }

  candidates.sort((a, b) => a.index - b.index)
  return candidates[0]?.result ?? noMatch
}

function routeResult(raw: string): PasteLinkResult | null {
  const link = raw.replace(trailingUrlPunctuation, '')
  let url: URL
  try {
    url = new URL(link, 'http://paste-link.invalid')
  } catch {
    return null
  }
  if (/^\/t\/(?:\d{8}-\d{3,}|\d+)(?:[?#].*)?$/.test(url.pathname + url.search + url.hash)) {
    return { kind: 'path', path: url.pathname + url.search + url.hash }
  }
  if (/^\/drafts\/dft_\d+(?:[?#].*)?$/.test(url.pathname + url.search + url.hash)) {
    return { kind: 'path', path: url.pathname + url.search + url.hash }
  }
  if (/^\/g\/tbg_[A-Za-z0-9_-]+(?:[?#].*)?$/.test(url.pathname + url.search + url.hash)) return guestLink
  return null
}

/** /t 前面可以是 URL 的域名/IP，也可以是文本开头；但不能藏在别的路径片段里。 */
function pathIsUrlPath(text: string, pathIndex: number): boolean {
  if (pathIndex === 0) return true
  let start = pathIndex
  while (start > 0 && !urlBoundary.test(text[start - 1])) start--
  const before = text.slice(start, pathIndex)
  if (/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(before)) return true
  if (/^localhost(?::\d+)?$/.test(before)) return true
  if (/^\[[0-9A-Fa-f:]+\](?::\d+)?$/.test(before)) return true
  return /^[^\s/:?#]+\.[^\s/:?#]+(?::\d+)?$/.test(before)
}
