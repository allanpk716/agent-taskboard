// 模板编辑（#42）：编辑模板 Markdown 源码，保存（新建 POST / 修改 PATCH 产生新
// 版本）；格式错误时服务端拒绝并报行号，页面显示在错误框里。另有解析出的字段、
// 历史版本与停用。/templates/new 是新建模式（先定 key）。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  authorText,
  createTemplate,
  deactivateTemplate,
  fetchTemplate,
  fetchTemplateVersions,
  updateTemplate,
  type TemplateDetail,
  type TemplateVersion,
} from './memberApi'
import { fmtShort } from '../shared/format'
import { Confirm, nav, toast } from './ui'

const STARTER = `---
名称: 新模板
类型: 任务
预设标签: []
---
## 要做什么？ {必填}
写清楚目标。
> 示例：整理一份对照表
`

const KIND_TEXT: Record<string, string> = { task: '任务', report: '反馈' }

export function TemplateEditPage({ templateKey }: { templateKey: string }) {
  const isNew = templateKey === 'new'
  const [detail, setDetail] = useState<TemplateDetail | null>(null)
  const [versions, setVersions] = useState<TemplateVersion[] | null>(null)
  const [key, setKey] = useState('')
  const [markdown, setMarkdown] = useState(isNew ? STARTER : '')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [confirmDeactivate, setConfirmDeactivate] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const reload = useCallback(
    async (k: string) => {
      try {
        const d = await fetchTemplate(k)
        setDetail(d)
        setMarkdown(d.markdown)
        setError(null)
        fetchTemplateVersions(k)
          .then(setVersions)
          .catch(() => setVersions([]))
      } catch (e) {
        setLoadError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取模板失败')
      }
    },
    [],
  )

  useEffect(() => {
    if (!isNew) void reload(templateKey)
  }, [isNew, templateKey, reload])

  async function save() {
    if (isNew && !key.trim()) {
      setError('模板 key 不能为空：用小写字母、数字和连字符，如 check-report')
      return
    }
    setBusy(true)
    setError(null)
    try {
      if (isNew) {
        const k = key.trim()
        await createTemplate(k, markdown)
        toast(`✓ 已创建模板 ${k}`)
        nav(`/templates/${encodeURIComponent(k)}`)
        return
      }
      const t = await updateTemplate(templateKey, markdown)
      toast(`✓ 已保存为 v${t.version}`)
      await reload(templateKey)
    } catch (e) {
      // 格式错误：服务端拒绝保存，message 带行号（如「第 4 行：……」）。
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function doDeactivate() {
    setConfirmDeactivate(false)
    try {
      await deactivateTemplate(templateKey)
      toast('已停用；不能用这个模板新建工单，已有工单不受影响')
      await reload(templateKey)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '停用失败')
    }
  }

  if (!isNew && loadError) {
    return (
      <div className="bpage">
        <div className="ecard">{loadError}</div>
        <div className="submitrow">
          <button className="btn ghost" onClick={() => nav('/templates')}>
            ← 模板列表
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="crumb">
        <a
          href="/templates"
          onClick={(e) => {
            e.preventDefault()
            nav('/templates')
          }}
        >
          ← 模板列表
        </a>
      </div>
      <div className="mcard">
        {isNew ? (
          <>
            <div className="sec-t" style={{ marginTop: 0 }}>新建模板</div>
            <div className="fld">
              <label htmlFor="tpl-key">模板 key（稳定标识，之后不能改）</label>
              <input
                id="tpl-key"
                placeholder="如 check-report"
                value={key}
                onChange={(e) => setKey(e.target.value)}
              />
              <div className="hint">用小写字母、数字和连字符；不能与已有模板重复。</div>
            </div>
          </>
        ) : detail ? (
          <>
            <div className="sec-t" style={{ marginTop: 0 }}>
              {detail.name} <span className="badge kind">{KIND_TEXT[detail.kind] ?? detail.kind}</span> v
              {detail.version}
              {!detail.active && <span className="badge closed">已停用</span>}
            </div>
            <div className="hint">
              最近修改 {fmtShort(detail.updated_at)} · {authorText(detail.updated_by)} · 预设标签{' '}
              {detail.preset_labels?.length ? detail.preset_labels.join('、') : '（无）'}
            </div>
          </>
        ) : (
          <div className="hint">加载中…</div>
        )}
      </div>

      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          模板源码（Markdown）
        </div>
        <textarea
          className="src"
          spellCheck={false}
          aria-label="模板源码"
          value={markdown}
          onChange={(e) => setMarkdown(e.target.value)}
        />
        <div className="btnrow">
          <button className="btn" onClick={() => void save()} disabled={busy}>
            {isNew ? '创建模板' : '保存（产生新版本）'}
          </button>
          {!isNew && detail?.active && (
            <button className="btn ghost" onClick={() => setConfirmDeactivate(true)}>
              停用模板
            </button>
          )}
        </div>
        {error && <div className="errbox">{error}</div>}
      </div>

      {!isNew && detail && (
        <>
          <div className="mcard">
            <div className="sec-t" style={{ marginTop: 0 }}>
              解析出的字段
            </div>
            <ul className="fldlist">
              {detail.fields.map((f) => (
                <li key={f.key}>
                  {f.title} {f.required && <i className="req">必填</i>}
                  {f.type === 'dimension' && <span className="hint"> · 维度 {f.dimension}</span>}
                  {f.type === 'radio' && <span className="hint"> · 单选</span>}
                </li>
              ))}
              {detail.fields.length === 0 && <li className="hint">（源码里还没有字段）</li>}
            </ul>
            {detail.agent_note && (
              <>
                <div className="sec-t">给 Agent 的说明（出现在填写指南和工单的「下一步」里）</div>
                <div className="notebox">{detail.agent_note}</div>
              </>
            )}
          </div>
          <div className="mcard">
            <div className="sec-t" style={{ marginTop: 0 }}>
              历史版本
            </div>
            {versions === null ? (
              <div className="hint">加载中…</div>
            ) : versions.length === 0 ? (
              <div className="hint">还没有版本。</div>
            ) : (
              versions.map((v) => (
                <details key={v.version} className="verrow">
                  <summary>
                    v{v.version} · {fmtShort(v.updated_at)} · {authorText(v.updated_by)}
                    {v.version === detail.version && <span className="mini">当前</span>}
                  </summary>
                  <pre className="versrc">{v.markdown}</pre>
                </details>
              ))
            )}
            <div className="hint">已有工单保留创建时模板版本的字段定义，模板以后修改不影响它们。</div>
          </div>
        </>
      )}

      {confirmDeactivate && detail && (
        <Confirm
          title="停用模板"
          body={
            <>
              停用后不能用「{detail.name}」新建工单；已有工单不受影响，仍按创建时的版本显示。模板不能删除。
            </>
          }
          yesLabel="停用"
          danger
          onYes={() => void doDeactivate()}
          onCancel={() => setConfirmDeactivate(false)}
        />
      )}
    </div>
  )
}
