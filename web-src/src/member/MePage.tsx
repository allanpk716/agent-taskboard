// 我的（#42）：账号信息、登出，名下成员 Agent 的 token 签发与撤销（token 只在
// 生成时显示一次，立即撤销即时生效）；管理员另有「管理」入口（成员账号、标签
// 治理；备份状态与更新在 #45）。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  changeOwnPassword,
  fetchAgentTokens,
  fetchClients,
  fetchClientLatest,
  issueAgentToken,
  logout,
  rotateAgentToken,
  revokeAgentToken,
  revokeClient,
  type AgentTokenRow,
  type IssuedToken,
  type ClientCredentialRow,
  type ClientLatest,
  type Me,
} from './memberApi'
import { fmtShort } from '../shared/format'
import { copyText, nav, toast } from './ui'
import { Confirm, Modal } from './ui'

export function MePage({ me, serverVersion }: { me: Me; serverVersion?: string }) {
  const [tokens, setTokens] = useState<AgentTokenRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [issuing, setIssuing] = useState(false)
  const [issued, setIssued] = useState<IssuedToken | null>(null)
  const [revoking, setRevoking] = useState<AgentTokenRow | null>(null)
  const [clients, setClients] = useState<ClientCredentialRow[] | null>(null)
  const [clientLatest, setClientLatest] = useState<ClientLatest | null | undefined>(undefined)
  const [revokingClient, setRevokingClient] = useState<ClientCredentialRow | null>(null)
  const [changingPassword, setChangingPassword] = useState(false)

  async function refresh() {
    try {
      setTokens(await fetchAgentTokens())
      setClients(await fetchClients())
      setClientLatest(await fetchClientLatest())
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '读取 Agent 列表失败')
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  async function revoke() {
    if (!revoking) return
    try {
      await revokeAgentToken(revoking.id)
      toast('✓ 已撤销；它的历史署名保留并标注「已停用」')
      await refresh()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '撤销失败')
    } finally {
      setRevoking(null)
    }
  }

  async function rotate(tk: AgentTokenRow) {
    try {
      const issued = await rotateAgentToken(tk.id)
      setIssued(issued)
      await refresh()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '换新失败')
    }
  }

  async function revokeConnectedClient() {
    if (!revokingClient) return
    try {
      await revokeClient(revokingClient.id)
      toast('✓ 已撤销这台电脑的凭证')
      await refresh()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '撤销电脑失败')
    } finally {
      setRevokingClient(null)
    }
  }

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="kv">
          <span>账号</span>
          <b>
            {me.name} · {me.is_admin ? '管理员' : '成员'}
          </b>
        </div>
        <div className="kv">
          <span>登录方式</span>
          <span>网页会话（HttpOnly cookie，算本人操作）</span>
        </div>
        <div className="btnrow">
          <button className="btn ghost" onClick={() => setChangingPassword(true)}>
            修改密码
          </button>
          <button className="btn ghost" onClick={() => void logout().finally(() => (window.location.href = '/login'))}>
            登出
          </button>
        </div>
      </div>

      {me.is_admin && (
        <div className="mcard">
          <div className="sec-t" style={{ marginTop: 0 }}>
            管理
          </div>
          <div className="hint">仅管理员可见。</div>
          <a
            className="toolrow"
            href="/admin"
            onClick={(e) => {
              e.preventDefault()
              nav('/admin')
            }}
          >
            🛡 管理页
            <span className="hint">成员账号 · 标签治理 · 备份 · 更新</span>
          </a>
        </div>
      )}

      <div className="mcard" id="client" data-testid="client-card">
        <div className="sec-t" style={{ marginTop: 0 }}>
          桌面客户端
        </div>
        {clientLatest === undefined ? (
          <div className="hint">…</div>
        ) : clientLatest ? (
          <>
            <div className="btnrow">
              <a className="btn" data-testid="client-download" href="/api/v1/client/download">
                下载 Windows 客户端 {clientLatest.version}
              </a>
            </div>
            <div className="hint">
              首次运行如遇 SmartScreen，先点「更多信息」，再点「仍要运行」；被杀毒软件拦截时，把
              taskboard-client.exe 加入信任。下载后 SHA256 应为 {clientLatest.sha256}。
            </div>
          </>
        ) : (
          <div className="hint" data-testid="client-unavailable">
            管理员尚未发布客户端。
          </div>
        )}
      </div>

      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          已连接的电脑
        </div>
        <table className="mini-tbl">
          <tbody>
            {(clients ?? []).map((c) => (
              <tr key={c.id} data-client={c.hostname}>
                <td>
                  <b>{c.hostname}</b>
                  <div className="hint">连接于 {fmtShort(c.created_at)}</div>
                </td>
                <td>
                  <div>{c.last_used_at ? `最近使用 ${fmtShort(c.last_used_at)}` : '未使用'}</div>
                  <div className="hint">
                    {c.client_version ? `taskboard-client ${c.client_version}` : '客户端版本未上报'}
                  </div>
                </td>
                <td>
                  <button className="btn sm ghost" onClick={() => setRevokingClient(c)}>
                    撤销
                  </button>
                </td>
              </tr>
            ))}
            {clients !== null && clients.length === 0 && (
              <tr>
                <td className="hint" colSpan={3}>
                  还没有已连接的电脑。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          Agent
        </div>
        <div className="hint">为名下的 Agent 签发具名 token</div>
        <div className="hint">
          可以有几个、分别署名「{me.name} 的 Agent · 名字」、分别撤销。Agent
          不能冒充本人，也不能签发 token；成员没有「本人 API token」——自己用命令行时，也建一个具名 Agent。
        </div>
        {error && <div className="errbox">{error}</div>}
        <table className="mini-tbl">
          <tbody>
            {(tokens ?? []).map((tk) => (
              <tr key={tk.id} data-token={tk.name}>
                <td>
                  <b>{tk.name}</b>
                  <div className="hint">创建于 {fmtShort(tk.created_at)}</div>
                </td>
                <td>
                  {tk.revoked_at ? (
                    <>
                      <span className="badge closed">已停用</span>
                      <div className="hint">历史署名保留并标注「已停用」</div>
                    </>
                  ) : (
                    <span className="badge open">使用中</span>
                  )}
                </td>
                <td>
                  {tk.revoked_at ? null : (
                    <>
                      <button className="btn sm ghost" onClick={() => void rotate(tk)}>
                        换新
                      </button>{' '}
                      <button className="btn sm ghost" onClick={() => setRevoking(tk)}>
                        撤销
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
            {tokens !== null && tokens.length === 0 && (
              <tr>
                <td className="hint" colSpan={3}>
                  还没有 Agent token。
                </td>
              </tr>
            )}
          </tbody>
        </table>
        <div className="btnrow">
          <button className="btn" onClick={() => setIssuing(true)}>
            ＋ 生成新 token
          </button>
        </div>
      </div>

      {serverVersion && (
        <div className="member-version" data-testid="member-version">
          工单板 v{serverVersion}
        </div>
      )}

      {issuing && (
        <IssueTokenDialog
          me={me}
          onClose={() => setIssuing(false)}
          onIssued={async (tok) => {
            setIssued(tok)
            setIssuing(false)
            await refresh()
          }}
        />
      )}
      {issued && <TokenOnceDialog token={issued} onClose={() => setIssued(null)} />}
      {changingPassword && (
        <ChangePasswordDialog onClose={() => setChangingPassword(false)} />
      )}
      {revoking && (
        <Confirm
          title={`撤销「${revoking.name}」的 token`}
          body={<>撤销后立即失效（401）；它写过的回复和事件保留原署名，并标注「已停用」。</>}
          yesLabel="撤销"
          danger
          onYes={() => void revoke()}
          onCancel={() => setRevoking(null)}
        />
      )}
      {revokingClient && (
        <Confirm
          title={`撤销「${revokingClient.hostname}」`}
          body={<>撤销后这台电脑的桌面客户端立即退出登录，需要重新连接才能继续提醒。</>}
          yesLabel="撤销"
          danger
          onYes={() => void revokeConnectedClient()}
          onCancel={() => setRevokingClient(null)}
        />
      )}
    </div>
  )
}

function ChangePasswordDialog({ onClose }: { onClose: () => void }) {
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)

  async function save() {
    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致')
      return
    }
    if (newPassword.length < 8) {
      setError('新密码至少 8 位')
      return
    }
    setBusy(true)
    try {
      await changeOwnPassword(oldPassword, newPassword)
      setDone(true)
      setError(null)
    } catch (e) {
      if (e instanceof ApiError) {
        const messages: Record<string, string> = {
          invalid_old_password: '原密码不正确',
          password_too_short: '新密码至少 8 位',
          password_unchanged: '新密码与旧密码相同',
        }
        setError(messages[e.code] ?? e.message)
      } else {
        setError('修改失败')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="修改密码" onClose={onClose}>
      {done ? (
        <>
          <div className="hint" data-password-changed>
            已修改，其他浏览器上的登录已退出
          </div>
          <div className="mrow">
            <span className="sp" />
            <button className="btn" onClick={onClose}>
              完成
            </button>
          </div>
        </>
      ) : (
        <>
          <label className="fldlab" htmlFor="old-password">
            旧密码
          </label>
          <input
            id="old-password"
            type="password"
            autoComplete="current-password"
            value={oldPassword}
            onChange={(e) => setOldPassword(e.target.value)}
          />
          <label className="fldlab" htmlFor="new-password">
            新密码
          </label>
          <input
            id="new-password"
            type="password"
            autoComplete="new-password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
          />
          <label className="fldlab" htmlFor="confirm-password">
            确认新密码
          </label>
          <input
            id="confirm-password"
            type="password"
            autoComplete="new-password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void save()}
          />
          <div className="hint">新密码至少 8 位，且不能与旧密码相同。</div>
          {error && <div className="errbox">{error}</div>}
          <div className="mrow">
            <button className="btn ghost" onClick={onClose}>
              取消
            </button>
            <span className="sp" />
            <button className="btn" onClick={() => void save()} disabled={busy}>
              保存
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}

function IssueTokenDialog({
  me,
  onClose,
  onIssued,
}: {
  me: Me
  onClose: () => void
  onIssued: (tok: IssuedToken) => void
}) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function issue() {
    if (!name.trim()) {
      setError('名字不能为空')
      return
    }
    setBusy(true)
    try {
      onIssued(await issueAgentToken(name.trim()))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '生成失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="生成 Agent token" onClose={onClose}>
      <label className="fldlab" htmlFor="tk-name">
        Agent 名字（会出现在署名里，如「{me.name} 的 Agent · 名字」）
      </label>
      <input
        id="tk-name"
        placeholder="例如：笔记本、工位、巡检"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && void issue()}
      />
      <div className="hint">token 会写进接入文案，只在生成后显示一次，请整段贴给这个 Agent。</div>
      {error && <div className="errbox">{error}</div>}
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" onClick={() => void issue()} disabled={busy}>
          生成
        </button>
      </div>
    </Modal>
  )
}

/** 接入文案只显示这一次：token 包含在其中，关闭后不再出现。 */
function TokenOnceDialog({ token, onClose }: { token: IssuedToken; onClose: () => void }) {
  return (
    <Modal title={`✓ 已生成「${token.name}」的 token（只显示这一次）`} onClose={onClose}>
      <div className="hint">
        把下面这段<b>接入文案</b>整段贴给这个 Agent，它会自己把 token 存好。<b>只需做一次</b>
        ，以后每张工单只要复制「工单指令」。
      </div>
      <pre className="handoff-pre" data-onboarding-preview>
        {token.onboarding_text}
      </pre>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
        <span className="sp" />
        <button
          className="btn"
          onClick={() =>
            void copyText(token.onboarding_text).then((ok) =>
              toast(ok ? '已复制' : '复制失败，请手动选择复制'),
            )
          }
        >
          复制接入文案
        </button>
      </div>
      <div className="hint">接入文案含明文 token，别转发给别人。弄丢了就在列表里点「换新」。</div>
    </Modal>
  )
}
