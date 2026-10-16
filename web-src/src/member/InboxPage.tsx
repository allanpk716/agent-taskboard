// 收件箱（#41）：「现在轮到我」的开启工单，卡片顶部是醒目的原因色条与文案，
// 一眼知道轮到我做什么。名下所有成员 Agent 共用这一份；未指派的工单不会
// 出现在收件箱里（要用查询接口找）。
import { useCallback, useEffect, useState } from 'react'
import { fetchInbox, reasonClass, type InboxResponse } from './memberApi'
import { nav, useInvalidate } from './ui'

export function InboxPage() {
  const [data, setData] = useState<InboxResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setData(await fetchInbox())
      setError(null)
    } catch (e: any) {
      setError(e?.message ?? '读取收件箱失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])
  useInvalidate(load)

  if (error) {
    return (
      <div className="bpage">
        <div className="ecard">{error}</div>
      </div>
    )
  }
  if (!data) {
    return (
      <div className="bpage">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="b-note">
        「现在轮到我」的开启工单，注明原因；名下所有成员 Agent 共用这一份。
      </div>
      {data.items.length === 0 ? (
        <div className="ecard">收件箱是空的。</div>
      ) : (
        data.items.map((it) => (
          <a
            key={it.number}
            className="tcard"
            href={`/t/${it.number}`}
            onClick={(e) => {
              e.preventDefault()
              nav(`/t/${it.number}`)
            }}
          >
            <span className={`strip ${reasonClass(it.reason)}`} aria-hidden="true" />
            <div className="tc-top">
              <span className="badge reason">{it.reason}</span>
              <span className="hint">{it.role === 'assignee' ? '我是指派人' : '我是发起人'}</span>
            </div>
            <div className="tc-title">{it.title}</div>
            <div className="tc-meta">{it.number}</div>
          </a>
        ))
      )}
    </div>
  )
}
