// 模板维护（#42）：模板列表（名称、类型、版本、最近修改、状态）＋ 新建与编辑。
// 模板是 Markdown + 少量约定：每次修改产生新版本、记录修改人，只能停用不能删除。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import { fetchTemplates, type TemplateSummary } from './memberApi'
import { nav } from './ui'

const KIND_TEXT: Record<string, string> = { task: '任务', report: '反馈' }

export function TemplatesPage() {
  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchTemplates()
      .then(setTemplates)
      .catch((e) => setError(e instanceof ApiError ? e.message : '读取模板失败'))
  }, [])

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          模板维护
        </div>
        <div className="hint">
          Markdown + 少量约定：YAML 头写名称、类型、预设标签；每个 ## 小节是一个字段。每次修改产生
          新版本，只能停用、不能删除。
        </div>
        <div className="btnrow">
          <button className="btn" onClick={() => nav('/templates/new')}>
            ＋ 新建模板
          </button>
        </div>
      </div>
      {error && <div className="ecard">{error}</div>}
      {templates === null && !error && <div className="ecard">加载中…</div>}
      {templates?.map((t) => (
        <div
          key={t.key}
          className="tcard"
          role="button"
          tabIndex={0}
          onClick={() => nav(`/templates/${encodeURIComponent(t.key)}`)}
          onKeyDown={(e) => e.key === 'Enter' && nav(`/templates/${encodeURIComponent(t.key)}`)}
        >
          <div className="tc-top">
            <b>{t.name}</b>
            <span className="badge kind">{KIND_TEXT[t.kind] ?? t.kind}</span>
            <span className="hint" style={{ marginLeft: 'auto' }}>
              v{t.version}
            </span>
          </div>
          <div className="tc-meta">{t.key}</div>
          <div className="tc-foot">
            {t.active ? <span className="badge open">使用中</span> : <span className="badge closed">已停用</span>}
            <span className="hint">编辑 ›</span>
          </div>
        </div>
      ))}
      {templates && templates.length === 0 && <div className="ecard">还没有模板。</div>}
    </div>
  )
}
