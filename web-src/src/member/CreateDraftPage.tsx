import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import type { AnyDraftView, CreateDraftRequest, CreateDraftView } from '../shared/types'
import { absoluteUrl } from '../shared/format'
import { Markdown } from '../shared/markdown'
import {
  discardDraft,
  fetchDraft,
  fetchMembers,
  sendDraft,
  updateCreateDraft,
  type MemberRef,
} from './memberApi'
import { Confirm, copyText, invalidate, nav, toast } from './ui'

type Stage =
  | { phase: 'loading' }
  | { phase: 'missing'; sent: boolean }
  | { phase: 'ready'; draft: CreateDraftView; request: CreateDraftRequest; editing: boolean }
  | { phase: 'sent'; draft: CreateDraftView }

function asCreateDraft(draft: AnyDraftView): CreateDraftView {
  return { ...draft, request: draft.request as CreateDraftRequest }
}

export function CreateDraftPage({ id }: { id: string }) {
  const [stage, setStage] = useState<Stage>({ phase: 'loading' })
  const [members, setMembers] = useState<MemberRef[]>([])
  const [busy, setBusy] = useState(false)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const [sent, setSent] = useState<{
    number: string
    url: string
    guestURL?: string
    shareText?: string
    signature: string
  } | null>(null)

  useEffect(() => {
    fetchDraft(id)
      .then((draft: AnyDraftView) => {
        if (draft.status === 'sent') {
          setStage({ phase: 'sent', draft: asCreateDraft(draft) })
          return
        }
        setStage({
          phase: 'ready',
          draft: asCreateDraft(draft),
          request: asCreateDraft(draft).request,
          editing: false,
        })
      })
      .catch((e) => {
        setStage({
          phase: 'missing',
          sent: e instanceof ApiError && e.code === 'draft_not_found' && e.status === 404,
        })
      })
  }, [id])

  useEffect(() => {
    fetchMembers()
      .then(setMembers)
      .catch(() => {})
  }, [])

  if (stage.phase === 'loading') return <div className="ecard">加载中…</div>
  if (stage.phase === 'missing') {
    return (
      <div className="draft-fallback">
        <b>{stage.sent ? '这份草稿已被丢弃' : '没有这份草稿'}</b>
        <span>它可能已被成员丢弃，或你不是这份草稿的主人。</span>
      </div>
    )
  }
  if (stage.phase === 'sent') {
    return (
      <div className="draft-fallback">
        <b>这份草稿已发出</b>
        {stage.draft.ticket && (
          <a
            className="btn"
            href={stage.draft.ticket.url}
            onClick={(e) => {
              e.preventDefault()
              nav(stage.draft.ticket!.url)
            }}
          >
            打开工单 {stage.draft.ticket.number}
          </a>
        )}
      </div>
    )
  }

  const { draft, request } = stage
  const dirty = JSON.stringify(request) !== JSON.stringify(draft.request)
  const isTemplate = !!request.template

  function patch(next: Partial<CreateDraftRequest>) {
    if (stage.phase !== 'ready') return
    setStage({ ...stage, request: { ...stage.request, ...next } })
  }

  function patchField(key: string, value: string) {
    patch({ fields: { ...(request.fields ?? {}), [key]: value } })
  }

  async function saveChanges(): Promise<CreateDraftView | null> {
    if (stage.phase !== 'ready') return null
    if (!dirty) return stage.draft
    setBusy(true)
    try {
      const updated = await updateCreateDraft(id, stage.draft.version, stage.request)
      setStage({ phase: 'ready', draft: updated, request: updated.request as CreateDraftRequest, editing: false })
      toast('修改已保存为新一版')
      invalidate()
      return updated
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '保存失败，请重试')
      return null
    } finally {
      setBusy(false)
    }
  }

  async function send() {
    const current = await saveChanges()
    if (!current) return
    setBusy(true)
    try {
      const result = await sendDraft(id, current.version)
      invalidate()
      setSent({
        number: result.result.number,
        url: result.result.url,
        guestURL: result.result.guest_link?.url,
        shareText: result.result.guest_link?.share_text,
        signature: result.signature,
      })
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '发出失败，请重试')
    } finally {
      setBusy(false)
    }
  }

  function instructionText() {
    const base = window.location.origin
    return `请修改我在 agent-taskboard 写的建单草稿 ${draft.id}（当前第 ${draft.version} 版）。
1. 凭证：用环境变量 TASKBOARD_TOKEN；读不到或返回 401 就停下来告诉我，不要用密码登录。
2. 先读当前草稿：curl -H "Authorization: Bearer $TASKBOARD_TOKEN" "${base}/api/v1/drafts/${draft.id}"
    我可能亲手改过，以最新一版为准，不要把我的改动改回去。
3. 按我下面的意见修改，然后整份写回：PUT ${base}/api/v1/drafts/${draft.id}，请求体 {"base_version": <刚读到的 version>, "request": {…完整建单内容…}}。
   返回 409 draft_version_conflict，说明你读完之后草稿又被改过；重新读，在最新版上改。
4. 改完把查看地址 ${absoluteUrl(draft.preview_url)} 发给我，说明这一版改了什么。发出或丢弃由我在网页上做，你做不了。

我的修改意见：`
  }

  async function copyInstruction() {
    const ok = await copyText(instructionText())
    toast(ok ? '修改指令已复制' : '复制失败，请手动选择文字复制')
  }

  return (
    <div className="create-draft-page">
      <div className="create-draft-banner">
        <b>草稿 · 未发布 · 只有你能看到</b>
        <span>不占工单编号；发出前不产生时间线事件</span>
      </div>
      <div className="create-draft-grid">
        <article className="create-draft-main" aria-label="建单草稿">
          <header>
            <div className="dmeta">编号在发出时分配 · 第 {draft.version} 版</div>
            {stage.editing ? (
              <input aria-label="标题" value={request.title} onChange={(e) => patch({ title: e.target.value })} />
            ) : (
              <h2>{request.title}</h2>
            )}
          </header>
          {stage.editing ? (
            isTemplate ? (
              // 模板草稿的 brief_md 发出时不会被采用：逐个字段编辑 fields，
              // 不给改 brief_md 的大文本框。
              (draft.template_fields ?? []).map((field) => {
                const value = request.fields?.[field.key] ?? ''
                const options = field.options ?? []
                return (
                  <label className="draft-edit-field" key={field.key}>
                    <span>
                      {field.title}
                      {field.required ? '（必填）' : ''}
                    </span>
                    {field.type !== 'text' && options.length > 0 ? (
                      <select
                        aria-label={field.title}
                        value={value}
                        onChange={(e) => patchField(field.key, e.target.value)}
                      >
                        <option value="">（未填）</option>
                        {(!value || options.includes(value) ? options : [...options, value]).map((opt) => (
                          <option key={opt} value={opt}>
                            {opt}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <textarea
                        aria-label={field.title}
                        value={value}
                        onChange={(e) => patchField(field.key, e.target.value)}
                      />
                    )}
                  </label>
                )
              })
            ) : (
              <textarea
                aria-label="说明"
                value={request.brief_md}
                onChange={(e) => patch({ brief_md: e.target.value })}
              />
            )
          ) : (
            <Markdown
              body={(isTemplate ? draft.rendered_brief_md : request.brief_md) || '（说明未填写）'}
              attachments={new Map()}
            />
          )}
          {isTemplate && draft.template_warning ? (
            <div className="warnbox">{draft.template_warning}</div>
          ) : null}
          {draft.warnings?.length ? (
            <div className="warnbox">必填项未填写：{draft.warnings.map((w) => w.field).join('、')}</div>
          ) : null}
          <div className="draft-actions create-draft-actions">
            <button type="button" className="btn ghost" onClick={() => void copyInstruction()}>
              复制修改指令
            </button>
            <button
              type="button"
              className="btn ghost"
              onClick={() => setStage({ ...stage, editing: !stage.editing })}
            >
              {stage.editing ? '取消' : '直接修改'}
            </button>
            {stage.editing && (
              <button type="button" className="btn" disabled={busy || !dirty} onClick={() => void saveChanges()}>
                保存草稿
              </button>
            )}
          </div>
        </article>

        <aside aria-label="发出时会做的事">
          <div className="create-action-card">
            <label htmlFor="draft-labels">标签</label>
            <input
              id="draft-labels"
              aria-label="标签"
              value={request.labels.join('，')}
              onChange={(e) =>
                patch({
                  labels: e.target.value
                    .split(/[,，]/)
                    .map((s) => s.trim())
                    .filter(Boolean),
                })
              }
            />
            <span className="hint">用逗号分隔；至少保留一个维度标签。</span>
          </div>
          <div className="create-action-card">
            <label htmlFor="draft-assignee">指派</label>
            <select
              id="draft-assignee"
              aria-label="指派"
              value={request.assignee}
              onChange={(e) => patch({ assignee: e.target.value })}
            >
              <option value="">暂不指派</option>
              {members
                .filter((m) => m.active !== false)
                .map((m) => (
                  <option key={m.id} value={m.name}>
                    {m.name}
                  </option>
                ))}
            </select>
          </div>
          <div className="create-action-card">
            <label htmlFor="draft-guest">访客链接</label>
            <label className="inrow-check">
              <input
                id="draft-guest"
                type="checkbox"
                checked={!!request.guest_link}
                onChange={(e) =>
                  patch({
                    guest_link: e.target.checked
                      ? { note: '', valid_days: 30, can_edit_brief: true }
                      : undefined,
                  })
                }
              />
              发出时创建
            </label>
            {request.guest_link && (
              <>
                <input
                  aria-label="访客链接备注"
                  value={request.guest_link.note}
                  onChange={(e) =>
                    patch({ guest_link: { ...request.guest_link!, note: e.target.value } })
                  }
                  placeholder="如：检验科-张三"
                />
                <select
                  aria-label="访客链接有效期"
                  value={request.guest_link.valid_days ?? 30}
                  onChange={(e) =>
                    patch({
                      guest_link: { ...request.guest_link!, valid_days: Number(e.target.value) },
                    })
                  }
                >
                  <option value={7}>7 天</option>
                  <option value={30}>30 天</option>
                  <option value={90}>90 天</option>
                </select>
                <label className="inrow-check">
                  <input
                    type="checkbox"
                    checked={request.guest_link.can_edit_brief}
                    onChange={(e) =>
                      patch({
                        guest_link: { ...request.guest_link!, can_edit_brief: e.target.checked },
                      })
                    }
                  />
                  可填写说明
                </label>
              </>
            )}
          </div>
        </aside>
      </div>

      <div className="create-draft-footer">
        <span>{dirty ? '有未保存的修改，发出前会保存为新一版' : `Agent ${draft.author.agent?.name ?? ''} 起草`}</span>
        <div className="draft-actions">
          <button type="button" className="btn danger ghost" disabled={busy} onClick={() => setConfirmDiscard(true)}>
            丢弃
          </button>
          <button type="button" className="btn" disabled={busy} onClick={() => void send()}>
            发出
          </button>
        </div>
      </div>

      {confirmDiscard && (
        <Confirm
          title="丢弃草稿？"
          body={<div>丢弃后不会创建工单，也不会留下访客链接。</div>}
          yesLabel="丢弃"
          danger
          onYes={() => {
            setConfirmDiscard(false)
            void discardDraft(id)
              .then(() => {
                toast('草稿已丢弃')
                setStage({ phase: 'missing', sent: true })
              })
              .catch(() => toast('丢弃失败，请重试'))
          }}
          onCancel={() => setConfirmDiscard(false)}
        />
      )}
      {sent && (
        <div className="mask show" role="dialog" aria-label="建单草稿已发出">
          <div className="modal">
            <div className="mtitle">草稿已发出</div>
            <div className="mbody">
              <p>
                已创建 <b>{sent.number}</b> · {sent.signature}
              </p>
              {sent.guestURL && (
                <>
                  <div className="sec-t">访客链接转发文案</div>
                  <div className="linkbox">{sent.shareText}</div>
                </>
              )}
              <div className="mrow">
                <button className="btn ghost" onClick={() => copyText(sent.shareText ?? '')}>
                  复制文案
                </button>
                <span className="sp" />
                <button
                  className="btn"
                  onClick={() => {
                    invalidate()
                    nav(sent.url)
                  }}
                >
                  打开新工单
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
