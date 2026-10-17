// 访客落地页（#66）：持「可填写说明」链接的访客第一次打开链接先看到这张
// 落地页——谁请他补充什么问题（工单标题）、大约要几分钟、几个问题几个
// 必填、可以传照片和日志、写到一半关掉也不丢（内容保存在这台设备上）。
// 「你的称呼」本机记住过就用记住的，否则预填访客链接的备注。「开始填写」
// 进分组填写页；「只想留言，不填了 →」直接进对话页。视觉参考
// prototype/guest-fill 的 c.html（原型代码不合并）。
import { useState } from 'react'
import { groupsOf, memberNameOf } from './GroupedBriefForm'
import { loadAuthor } from './draft'
import type { TicketView } from '../shared/types'

export function LandingPage({
  ticket,
  onStart,
  onTalk,
}: {
  ticket: TicketView
  /** 「开始填写」进分组填写页；带上落地页里改过的称呼作署名初值。 */
  onStart: (author: string) => void
  /** 「只想留言，不填了 →」直接进对话页。 */
  onTalk: () => void
}) {
  const member = memberNameOf(ticket)
  const fields = groupsOf(ticket).flatMap((g) => g.fields)
  const required = fields.filter((f) => f.required).length
  // 自由描述是一整段话（大约 3 分钟）；模板按问题数估几分钟（封在 3–10）。
  const free = (ticket.brief.template_fields ?? []).length === 0
  const minutes = free ? 3 : Math.max(3, Math.min(10, Math.round(fields.length * 0.55)))
  const [author, setAuthor] = useState(loadAuthor() || ticket.viewer.link_note || '')

  return (
    <div className="guest-landing">
      <div className="landing-card">
        <div className="landing-lead muted">{member} 请你补充一个问题的情况</div>
        <h1 className="landing-title">{ticket.title}</h1>
        <div className="landing-info">
          ⏱ {free ? '大约 3 分钟，写一段说明' : `大约 ${minutes} 分钟，${fields.length} 个问题（${required} 个必填）`}
          <br />📎 可以上传照片、截图和日志
          <br />💾 写到一半关掉也没关系，内容保存在这台设备上
        </div>
        <div className="landing-author">
          <label htmlFor="landing-author">你的称呼（{member}会看到）</label>
          <input
            id="landing-author"
            value={author}
            placeholder="如：检验科 张三"
            onChange={(e) => setAuthor(e.target.value)}
          />
        </div>
        <button type="button" className="btn big block start-btn" onClick={() => onStart(author)}>
          开始填写
        </button>
        <div className="landing-talk">
          <button type="button" className="linkish" onClick={onTalk}>
            只想留言，不填了 →
          </button>
        </div>
      </div>
    </div>
  )
}
