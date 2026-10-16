// 我的（#42）：账号信息与登出；Agent token 的签发与撤销在 #42 加入。
// 管理员另有系统状态面板（#45：备份状态与版本更新）。
import { logout, type Me } from './memberApi'
import { SystemPanel } from './admin/SystemPanel'

export function MePage({ me }: { me: Me }) {
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
      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          我的成员 Agent（API token）
        </div>
        <div className="hint">为名下的 Agent 签发具名 token、分别撤销（#42 加入）。</div>
      </div>
      {me.is_admin && <SystemPanel />}
    </div>
  )
}
