import type { ReactNode } from 'react'
import type { GuestAccessCodeAction, UnassignedGuestLink } from './memberApi'

export type GuestAccessCodeCardMode = GuestAccessCodeAction

interface Props {
  mode: GuestAccessCodeCardMode
  guestName: string
  sourceLinks?: UnassignedGuestLink[]
  selectedSourceId?: number
  generateSelected?: boolean
  onSelectSource?: (id: number) => void
  onSelectGenerate?: () => void
  children?: ReactNode
}

/** 批量归属的访问码卡片：展示结果，或在需要时选择沿用来源 / 生成新码。 */
export function GuestAccessCodeCard({
  mode,
  guestName,
  sourceLinks = [],
  selectedSourceId,
  generateSelected,
  onSelectSource,
  onSelectGenerate,
  children,
}: Props) {
  const person = guestName.includes('-') ? guestName.split('-').slice(1).join('-') : guestName
  return (
    <div className="guest-access-card" data-testid="guest-access-code-card">
      <div className="sec-t" style={{ marginTop: 0 }}>
        访问码 <span className="hint">{modeText(mode, person)}</span>
      </div>
      {mode === 'keep_guest' && (
        <div className="guest-access-choice selected">
          <strong>保留 {person} 的访问码</strong>
          <span className="hint">已登录设备继续可用；被归入链接的旧访问码和设备作废。</span>
        </div>
      )}
      {mode === 'remain_unclaimed' && (
        <div className="guest-access-choice selected">
          <strong>访客保持未认领</strong>
          <span className="hint">TA 第一次打开主页或链接时再设置新访问码。</span>
        </div>
      )}
      {mode === 'choice_required' && (
        <>
          {onSelectSource &&
            sourceLinks.map((link) => (
              <label
                key={link.id}
                className={`guest-access-choice${selectedSourceId === link.id ? ' selected' : ''}`}
                data-source-id={link.id}
              >
                <input
                  type="radio"
                  name="guest-access-source"
                  checked={selectedSourceId === link.id}
                  onChange={() => onSelectSource(link.id)}
                />
                <span>
                  <strong>沿用 {link.ticket_number} 的访问码</strong>
                  <span className="hint">
                    {link.claim_name || link.note} · 最近使用{' '}
                    {link.last_used_at ? new Date(link.last_used_at).toLocaleString() : '未记录'}
                  </span>
                </span>
              </label>
            ))}
          {onSelectGenerate && (
            <label className={`guest-access-choice${generateSelected ? ' selected' : ''}`}>
              <input
                type="radio"
                name="guest-access-source"
                checked={!!generateSelected}
                onChange={onSelectGenerate}
              />
              <span>
                <strong>生成新的访问码</strong>
                <span className="hint">6 位数字，只显示一次。</span>
              </span>
            </label>
          )}
        </>
      )}
      {mode === 'reuse_source_link' && (
        <div className="guest-access-choice selected">
          <strong>沿用所选老链接的访问码</strong>
          <span className="hint">来源链接的认领状态、称呼、访问码、锁定和设备都转给访客。</span>
        </div>
      )}
      {mode === 'generate' && (
        <div className="guest-access-choice selected">
          <strong>生成新的访问码</strong>
          <span className="hint">访客变为已认领，称呼取访客名字；旧访问码和设备作废。</span>
        </div>
      )}
      {children}
    </div>
  )
}

function modeText(mode: GuestAccessCodeCardMode, person: string): string {
  if (mode === 'keep_guest') return `保留${person}的现有访问码`
  if (mode === 'choice_required') return '需要选择沿用或生成'
  if (mode === 'reuse_source_link') return '沿用所选老链接的访问码'
  if (mode === 'generate') return '生成只显示一次的新码'
  return '访客保持未认领'
}
