// 访客详情档案页（#338）：档案头部 + 工单/访问/管理三个标签。
// 工单只列当前成员可见范围，轮到访客的置顶并注明原因；访问集中访问码、
// 设备与主页操作；管理保留改名、停用/恢复与合并（规则不变）。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  copyGuestHomeText,
  disableGuest,
  enableGuest,
  fetchGuestDetail,
  fetchGuests,
  mergeGuest,
  previewGuestMerge,
  previewGuestAccessCodeReset,
  renameGuest,
  resetGuestAccessCodeForGuest,
  resetGuestHomeKey,
  type GuestAccessCodeResetPreview,
  type GuestDetail,
  type GuestMergePreview,
  type GuestSummary,
} from './memberApi'
import { ResetAccessResultDialog } from './ResetAccessDialog'
import { guestLinkStatusText } from './guestLinkStatus'
import { Confirm, Modal, copyText, nav, toast } from './ui'
import { absoluteUrl, fmtDateCN } from '../shared/format'

export function GuestDetailPage({ guestId }: { guestId: string }) {
  const id = Number(guestId)
  const [detail, setDetail] = useState<GuestDetail | null>(null)
  const [tab, setTab] = useState<'tickets' | 'access' | 'manage'>('tickets')
  const [error, setError] = useState<string | null>(null)
  const [renaming, setRenaming] = useState(false)
  const [newName, setNewName] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmDisable, setConfirmDisable] = useState(false)
  const [confirmEnable, setConfirmEnable] = useState(false)
  const [confirmCodeReset, setConfirmCodeReset] = useState(false)
  const [confirmHomeReset, setConfirmHomeReset] = useState(false)
  const [mergeOpen, setMergeOpen] = useState(false)
  const [mergeQuery, setMergeQuery] = useState('')
  const [mergeTargets, setMergeTargets] = useState<GuestSummary[] | null>(null)
  const [mergeTargetsError, setMergeTargetsError] = useState<string | null>(null)
  const [mergeTargetID, setMergeTargetID] = useState(0)
  const [mergePreview, setMergePreview] = useState<GuestMergePreview | null>(null)
  const [mergePreviewError, setMergePreviewError] = useState<string | null>(null)
  const [confirmMerge, setConfirmMerge] = useState(false)
  const [codePreview, setCodePreview] = useState<GuestAccessCodeResetPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [resetResult, setResetResult] = useState<{
    action: 'code' | 'home'
    url: string
    urlLabel: string
    accessCode?: string
    resetText: string
  } | null>(null)

  const refresh = useCallback(() => {
    fetchGuestDetail(id)
      .then(setDetail)
      .catch((e) => setError(e instanceof ApiError ? e.message : '读取访客详情失败'))
  }, [id])

  useEffect(() => {
    refresh()
  }, [refresh])

  // 打开重置访问码确认时先取影响预览（作废设备数 / 涉及工单数）。
  useEffect(() => {
    if (!confirmCodeReset || codePreview || previewError) return
    let cancelled = false
    previewGuestAccessCodeReset(id)
      .then((got) => {
        if (!cancelled) setCodePreview(got)
      })
      .catch((e) => {
        if (!cancelled) {
          setPreviewError(e instanceof ApiError ? e.message : '影响预览加载失败，请稍后重试')
        }
      })
    return () => {
      cancelled = true
    }
  }, [confirmCodeReset, codePreview, previewError, id])

  // 合并入口只列仍可被选择的目标访客；已停用或已并入者不能接收来源访客的链接。
  useEffect(() => {
    if (!mergeOpen) return
    let alive = true
    fetchGuests(mergeQuery)
      .then((list) => {
        if (alive) {
          setMergeTargets(
            list.filter((g) => g.id !== id && !g.disabled_at && !g.merged_into_guest_id),
          )
        }
      })
      .catch(() => {
        if (alive) {
          setMergeTargets([])
          setMergeTargetsError('读取可选访客失败，请稍后重试')
        }
      })
    return () => {
      alive = false
    }
  }, [mergeOpen, mergeQuery, id])

  // 选定目标后先展示影响面；预览成功后才允许进入二次确认。
  useEffect(() => {
    if (!mergeOpen || !mergeTargetID) return
    let alive = true
    setMergePreview(null)
    setMergePreviewError(null)
    previewGuestMerge(id, mergeTargetID)
      .then((got) => {
        if (alive) setMergePreview(got)
      })
      .catch((e) => {
        if (alive) {
          setMergePreviewError(
            e instanceof ApiError ? `${e.code}：${e.message}` : '合并影响预览加载失败',
          )
        }
      })
    return () => {
      alive = false
    }
  }, [mergeOpen, mergeTargetID, id])

  if (error) {
    return (
      <div className="bpage">
        <div className="ecard">{error}</div>
        <div className="mcard">
          <button className="btn ghost" onClick={() => nav('/guests')}>
            返回访客名单
          </button>
        </div>
      </div>
    )
  }
  if (!detail) {
    return (
      <div className="bpage">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  const guest = detail.guest
  const disabled = !!guest.disabled_at
  const merged = !!guest.merged_at

  async function submitRename() {
    const name = newName.trim()
    if (!name) {
      toast('请填写去空白后的访客名字')
      return
    }
    setBusy(true)
    try {
      await renameGuest(id, name)
      toast(`✓ 已改名为「${name}」；新回复将使用新名字，历史署名保持原样`)
      setRenaming(false)
      setNewName('')
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '改名失败')
    } finally {
      setBusy(false)
    }
  }

  async function copyHome() {
    try {
      const result = await copyGuestHomeText(id)
      const ok = await copyText(result.share_text)
      toast(ok ? `✓ ${guest.name}的主页文案已复制（不含访问码）` : '复制失败，请手动选择文字复制')
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '复制主页文案失败')
    }
  }

  async function toggleDisabled(enable: boolean) {
    setBusy(true)
    try {
      if (enable) {
        await enableGuest(id)
        toast(`✓ 已恢复「${guest.name}」，未过期链接按原有效期重新生效`)
      } else {
        await disableGuest(id)
        toast(`✓ 已停用「${guest.name}」，主页与名下全部链接一起失效`)
      }
      setConfirmDisable(false)
      setConfirmEnable(false)
      refresh()
      window.dispatchEvent(new Event('tb:invalidate'))
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : enable ? '恢复失败' : '停用失败')
    } finally {
      setBusy(false)
    }
  }

  async function resetCode() {
    setBusy(true)
    try {
      const res = await resetGuestAccessCodeForGuest(id)
      setResetResult({
        action: 'code',
        url: res.home_url,
        urlLabel: '访客主页',
        accessCode: res.access_code,
        resetText: res.reset_text.replaceAll(res.home_url, absoluteUrl(res.home_url)),
      })
      setConfirmCodeReset(false)
      setCodePreview(null)
      setPreviewError(null)
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '重置访问码失败')
    } finally {
      setBusy(false)
    }
  }

  async function resetHome() {
    setBusy(true)
    try {
      const res = await resetGuestHomeKey(id)
      setResetResult({
        action: 'home',
        url: res.home_url,
        urlLabel: '新的主页地址',
        resetText: res.share_text.replaceAll(res.home_url, absoluteUrl(res.home_url)),
      })
      setConfirmHomeReset(false)
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '重置主页地址失败')
    } finally {
      setBusy(false)
    }
  }

  async function submitMerge() {
    if (!mergeTargetID) return
    setBusy(true)
    try {
      const result = await mergeGuest(id, mergeTargetID)
      setMergeOpen(false)
      toast(
        `✓ 已把 ${result.link_count} 条链接并入目标访客，` +
          `${result.device_count} 台来源访客设备已作废；合并不能撤回`,
      )
      window.dispatchEvent(new Event('tb:invalidate'))
      nav(`/guests/${result.target_guest_id}`)
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '合并访客失败')
    } finally {
      setBusy(false)
    }
  }

  if (resetResult) {
    return <ResetAccessResultDialog result={resetResult} onClose={() => setResetResult(null)} />
  }

  const accessCodeStatus = merged
    ? '已失效'
    : disabled
      ? '已停用'
      : guest.access_code_locked_at
        ? '已认领 · 输码已锁定'
        : guest.claimed_at
          ? '已认领'
          : '未认领'

  return (
    <div className="bpage guest-detail">
      <div className="mcard guest-profile">
        <h2 className="guest-profile-name">{guest.name}</h2>
        <div className="guest-profile-status">
          {merged ? (
            <span className="badge closed">已并入</span>
          ) : disabled ? (
            <span className="badge closed">已停用</span>
          ) : guest.claimed_at ? (
            <span className="badge open">已认领</span>
          ) : (
            <span className="badge">未认领</span>
          )}
          {!merged && !disabled && guest.access_code_locked_at && (
            <span className="badge myturn">访问码已锁定</span>
          )}
        </div>
        <div className="hint">
          {merged
            ? `已于 ${fmtDateCN(guest.merged_at!)} 并入另一位访客；主页、访问码和设备已失效`
            : disabled
              ? `停用于 ${fmtDateCN(guest.disabled_at!)}；主页与名下全部链接一起失效`
              : guest.claimed_at
                ? `已认领${guest.claim_name ? ` · ${guest.claim_name}` : ''} · ${fmtDateCN(guest.claimed_at)}`
                : '未认领'}
          {' · '}
          {guest.link_count} 条链接
        </div>
      </div>

      <div className="guest-tabs" role="tablist" aria-label="访客档案">
        {(
          [
            ['tickets', '工单'],
            ['access', '访问'],
            ['manage', '管理'],
          ] as const
        ).map(([key, label]) => (
          <button
            type="button"
            key={key}
            role="tab"
            aria-selected={tab === key}
            data-testid={`guest-tab-${key}`}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'tickets' && (
        <div className="mcard" role="tabpanel" data-testid="guest-panel-tickets">
          <div className="sec-t" style={{ marginTop: 0 }}>
            名下工单{' '}
            <span className="hint">
              {detail.tickets.length} 张 · 只列你可见范围内的工单
            </span>
          </div>
          {detail.tickets.length === 0 ? (
            <div className="hint">你可见范围内没有这位访客名下的工单。</div>
          ) : (
            detail.tickets.map((t) => (
              <div className="guest-ticket" key={t.number} data-ticket-number={t.number}>
                {t.waiting_on_guest && (
                  <div className="guest-turn">
                    <span className="badge myturn">轮到访客</span>
                    <span className="hint">{t.waiting_reason}</span>
                  </div>
                )}
                <a
                  href={`/t/${t.number}`}
                  onClick={(e) => {
                    e.preventDefault()
                    nav(`/t/${t.number}`)
                  }}
                >
                  <span className={`badge ${t.status === 'closed' ? 'closed' : 'open'}`}>
                    {t.status === 'closed' ? '已关闭' : '开启'}
                  </span>
                  {t.title}
                </a>
                <div className="guest-links">
                  {t.links.map((l) => (
                    <span className="hint" key={l.id}>
                      「{l.note}」{l.can_edit_brief ? ' · 可填写说明' : ''}
                      {' · '}
                      {guestLinkStatusText(l)}
                      {!l.revoked_at ? ` · 到 ${fmtDateCN(l.expires_at)}` : ''}
                    </span>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {tab === 'access' && (
        <div role="tabpanel" data-testid="guest-panel-access">
          <div className="mcard">
            <div className="sec-t" style={{ marginTop: 0 }}>
              访问码 <span className="hint">{accessCodeStatus}</span>
            </div>
            <div className="kv">
              <span>状态</span>
              <b>{accessCodeStatus}</b>
            </div>
            <div className="kv">
              <span>已登录设备</span>
              <b>{merged ? '0 台（已失效）' : `${guest.device_count} 台`}</b>
            </div>
            <div className="kv">
              <span>适用范围</span>
              <b>这位访客名下所有访客链接</b>
            </div>
            {!merged && (
              <div className="actrow">
                <button
                  className="btn ghost"
                  onClick={() => {
                    setCodePreview(null)
                    setPreviewError(null)
                    setConfirmCodeReset(true)
                  }}
                >
                  重置访问码
                </button>
              </div>
            )}
            <div className="hint">
              重置后这位访客的全部已登录设备作废，新访问码只显示一次；主页地址和链接不变。
            </div>
          </div>
          <div className="mcard">
            <div className="sec-t" style={{ marginTop: 0 }}>
              访客主页 <span className="hint">长期地址</span>
            </div>
            <div className="hint">长期有效的带凭证地址，列出名下链接仍有效的工单。</div>
            {!merged && (
              <div className="actrow">
                <button className="btn ghost" onClick={() => void copyHome()}>
                  复制主页文案
                </button>
                <button className="btn ghost" onClick={() => setConfirmHomeReset(true)}>
                  重置主页地址
                </button>
              </div>
            )}
            <div className="hint">
              复制的是发给访客的转发文案；文案不包含访问码。重置后旧主页地址立即失效。
            </div>
          </div>
        </div>
      )}

      {tab === 'manage' && (
        <div className="mcard" role="tabpanel" data-testid="guest-panel-manage">
          <div className="sec-t" style={{ marginTop: 0 }}>基础管理</div>
          <div className="guest-manage-row">
            <div className="guest-manage-top">
              <strong>改名</strong>
              {!renaming && (
                <button
                  className="btn ghost"
                  onClick={() => {
                    setNewName(guest.name)
                    setRenaming(true)
                  }}
                >
                  改名
                </button>
              )}
            </div>
            <p>名字写在访客上，改名后名下所有访客链接一起沿用新名字；历史署名保持原样。</p>
            {renaming && (
              <div className="inrow" style={{ marginTop: 10 }}>
                <input
                  aria-label="访客新名字"
                  value={newName}
                  placeholder="如：检验科-张三"
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && void submitRename()}
                />
                <button className="btn" onClick={() => void submitRename()} disabled={busy}>
                  保存
                </button>
                <button className="btn ghost" onClick={() => setRenaming(false)}>
                  取消
                </button>
              </div>
            )}
          </div>
          {!merged && (
            <div className="guest-manage-row">
              <div className="guest-manage-top">
                <strong>{disabled ? '恢复' : '停用'}</strong>
                {disabled ? (
                  <button className="btn ghost" onClick={() => setConfirmEnable(true)} disabled={busy}>
                    恢复
                  </button>
                ) : (
                  <button className="btn ghost danger" onClick={() => setConfirmDisable(true)} disabled={busy}>
                    停用
                  </button>
                )}
              </div>
              <p>
                {disabled
                  ? '恢复后主页重新可用，名下未撤销、未过期的链接按原有效期重新生效。'
                  : '停用后访客主页和名下全部链接一起失效；需要访客补充的工单回到跟进人。'}
              </p>
            </div>
          )}
          {!merged && (
            <div className="guest-manage-row">
              <div className="guest-manage-top">
                <strong>合并到另一位访客</strong>
                <button
                  className="btn ghost"
                  onClick={() => {
                    setMergeTargets(null)
                    setMergeTargetsError(null)
                    setMergeTargetID(0)
                    setMergePreview(null)
                    setMergePreviewError(null)
                    setConfirmMerge(false)
                    setMergeQuery('')
                    setMergeOpen(true)
                  }}
                  disabled={busy}
                >
                  合并到另一位访客
                </button>
              </div>
              <p>把这位访客的链接并入目标访客；来源访客的访问码和设备作废，合并不能撤回。</p>
            </div>
          )}
          <div className="hint">
            只有访客建立者、管理员或名下链接的管理人可以管理这位访客；停用和恢复只能在网页上操作。
          </div>
        </div>
      )}

      {confirmDisable && (
        <Confirm
          title="停用这位访客？"
          body={`停用后「${guest.name}」的访客主页和名下全部链接一起失效，需要这位访客补全说明的工单会回到指派人。各链接的撤销与有效期字段不改写；恢复后未过期的链接按原有效期重新生效。`}
          yesLabel="停用"
          onYes={() => void toggleDisabled(false)}
          onCancel={() => setConfirmDisable(false)}
          danger
        />
      )}
      {confirmEnable && (
        <Confirm
          title="恢复这位访客？"
          body={`恢复后「${guest.name}」的主页重新可用，名下未撤销、未过期的链接按原有效期重新生效；已过期的链接仍然失效。`}
          yesLabel="恢复"
          onYes={() => void toggleDisabled(true)}
          onCancel={() => setConfirmEnable(false)}
        />
      )}
      {confirmCodeReset && (
        <Modal title="🔐 重置访问码" onClose={() => setConfirmCodeReset(false)} wide>
          <p className="reset-lead">
            将按访客「{guest.name}」重置访问码：主页与名下全部链接共用同一个访问码。
          </p>
          {codePreview ? (
            <div className="reset-info">
              影响范围：作废 {codePreview.device_count} 台设备，涉及 {codePreview.ticket_count} 张工单
              {codePreview.access_code_locked ? '，并解除当前的输错锁定' : ''}。
            </div>
          ) : previewError ? (
            <div className="warnbox">{previewError}</div>
          ) : (
            <div className="hint">正在加载影响范围…</div>
          )}
          <div className="warnbox">
            这位访客的所有已登录设备（主页、名下全部链接与对方的 Agent）会立即失效，连续输错造成的锁定会解除；
            将生成新的 6 位访问码，链接地址与主页地址保持不变。新码只显示一次。
          </div>
          <div className="mrow">
            <button className="btn ghost" onClick={() => setConfirmCodeReset(false)}>
              取消
            </button>
            <span className="sp" />
            <button className="btn" onClick={() => void resetCode()} disabled={busy}>
              重置访问码
            </button>
          </div>
        </Modal>
      )}
      {confirmHomeReset && (
        <Confirm
          title="🔄 重置主页地址？"
          body={`「${guest.name}」的旧主页地址立即失效；访问码、已登录设备和名下链接都不受影响。将生成新的主页地址。`}
          yesLabel="重置主页地址"
          onYes={() => void resetHome()}
          onCancel={() => setConfirmHomeReset(false)}
        />
      )}
      {mergeOpen && (
        <Modal title="合并到另一位访客" onClose={() => setMergeOpen(false)} wide>
          {!confirmMerge ? (
            <>
              <div className="fld">
                <label>
                  目标访客<i className="req">必填</i>
                </label>
                <div className="inrow">
                  <input
                    aria-label="搜索目标访客"
                    type="search"
                    placeholder="按名字搜索目标访客"
                    value={mergeQuery}
                    onChange={(e) => setMergeQuery(e.target.value)}
                  />
                  <select
                    className="sel"
                    aria-label="选择目标访客"
                    value={mergeTargetID ? String(mergeTargetID) : ''}
                    onChange={(e) => setMergeTargetID(Number(e.target.value))}
                  >
                    <option value="">— 选择目标访客 —</option>
                    {(mergeTargets ?? []).map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                        {g.claim_name ? `（已认领 · ${g.claim_name}）` : g.claimed_at ? '（已认领）' : ''}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
              {mergeTargetsError ? (
                <div className="warnbox">{mergeTargetsError}</div>
              ) : mergeTargets === null ? (
                <div className="hint">正在读取可选访客…</div>
              ) : mergeTargetID === 0 ? (
                <div className="hint">先选择「{guest.name}」要并入哪一位访客。</div>
              ) : mergePreview ? (
                <div className="reset-info">
                  将把「{mergePreview.source_guest_name}」的 {mergePreview.link_count} 条链接改归
                  「{mergePreview.target_guest_name}」，并作废来源访客的 {mergePreview.device_count} 台设备。
                </div>
              ) : mergePreviewError ? (
                <div className="warnbox">{mergePreviewError}</div>
              ) : (
                <div className="hint">正在加载影响范围…</div>
              )}
              <div className="hint">
                目标访客的访问码、已登录设备和主页地址保持不变；来源访客的主页、访问码和设备会全部失效。
              </div>
              <div className="mrow">
                <button className="btn ghost" onClick={() => setMergeOpen(false)}>
                  取消
                </button>
                <span className="sp" />
                <button
                  className="btn"
                  onClick={() => setConfirmMerge(true)}
                  disabled={busy || !mergeTargetID || !mergePreview}
                >
                  下一步：确认合并
                </button>
              </div>
            </>
          ) : (
            <>
              <p className="reset-lead">
                确认把「{guest.name}」并入「{mergePreview?.target_guest_name ?? '目标访客'}」？
              </p>
              <div className="reset-info">
                改归 {mergePreview?.link_count ?? 0} 条链接；作废来源访客 {mergePreview?.device_count ?? 0} 台设备。
              </div>
              <div className="warnbox">
                这是二次确认：合并不能撤回。来源访客不会再出现在访客名单，也不能再被选作建链接的访客。
              </div>
              <div className="mrow">
                <button className="btn ghost" onClick={() => setConfirmMerge(false)}>
                  返回上一步
                </button>
                <span className="sp" />
                <button className="btn danger" onClick={() => void submitMerge()} disabled={busy}>
                  确认合并
                </button>
              </div>
            </>
          )}
        </Modal>
      )}
      {/* 底部固定动作（#340，用户故事 6）：带访客 id 进建单向导并跳过选访客。 */}
      <div className="guest-detail-footer">
        <a
          className="btn big"
          href={`/tickets/new?guest=${guest.id}`}
          onClick={(e) => {
            e.preventDefault()
            nav(`/tickets/new?guest=${guest.id}`)
          }}
        >
          ✉️ 给 TA 发新工单
        </a>
      </div>
    </div>
  )
}
