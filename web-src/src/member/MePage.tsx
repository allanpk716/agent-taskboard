// 我的（#42）：账号信息、登出，名下成员 Agent 的 token 签发与撤销（token 只在
// 生成时显示一次，立即撤销即时生效）；管理员另有「管理」入口（成员账号、标签
// 治理；备份状态与更新在 #45）。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  fetchAgentTokens,
  fetchClients,
  fetchClientLatest,
  issueAgentToken,
  logout,
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

export function MePage({ me }: { me: Me }) {
  const [tokens, setTokens] = useState<AgentTokenRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [issuing, setIssuing] = useState(false)
  const [issued, setIssued] = useState<IssuedToken | null>(null)
  const [revoking, setRevoking] = useState<AgentTokenRow | null>(null)
  const [clients, setClients] = useState<ClientCredentialRow[] | null>(null)
  const [clientLatest, setClientLatest] = useState<ClientLatest | null | undefined>(undefined)
  const [revokingClient, setRevokingClient] = useState<ClientCredentialRow | null>(null)

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
          我的成员 Agent（API token）
        </div>
        <div className="hint">
          为名下的 Agent 签发具名 token：可以有几个、分别署名「{me.name} 的 Agent ·
          名字」、分别撤销。Agent 不能冒充本人，也不能签发 token；成员没有「本人 API
          token」——自己用命令行时，也建一个具名 Agent。
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
                    <button className="btn sm ghost" onClick={() => setRevoking(tk)}>
                      撤销
                    </button>
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
      {issued && <TokenOnceDialog me={me} token={issued} onClose={() => setIssued(null)} />}
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
      <div className="hint">token 只在生成时显示一次，请立刻交给这个 Agent 的配置。</div>
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

/** token 只显示这一次：显示 + 复制，关闭后不再出现。 */
function TokenOnceDialog({ me, token, onClose }: { me: Me; token: IssuedToken; onClose: () => void }) {
  return (
    <Modal title="✓ token 已生成（只显示这一次）" onClose={onClose}>
      <div className="fldlab">
        {me.name} 的 Agent · {token.name}
      </div>
      <div className="linkbox mono" data-token-value>
        {token.token}
      </div>
      <div className="hint">用它做 Bearer token 调用 API；撤销立即失效，历史署名保留。</div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
        <span className="sp" />
        <button
          className="btn"
          onClick={() =>
            void copyText(token.token).then((ok) => toast(ok ? '已复制' : '复制失败，请手动选择复制'))
          }
        >
          复制
        </button>
      </div>
    </Modal>
  )
}
