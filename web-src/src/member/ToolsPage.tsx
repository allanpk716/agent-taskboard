// 工具页（#42）：标签字典、模板维护、管理页。本张先放占位。
export function ToolsPage() {
  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          工具
        </div>
        <a
          className="toolrow"
          href="/tools"
          onClick={(e) => {
            e.preventDefault()
          }}
        >
          🧾 标签字典与别名
          <span className="hint">维度、值、别名与 resolve（#42 加入）</span>
        </a>
        <a
          className="toolrow"
          href="/tools"
          onClick={(e) => {
            e.preventDefault()
          }}
        >
          📄 模板维护
          <span className="hint">Markdown 模板 · 版本 · 停用（#42 加入）</span>
        </a>
        <a
          className="toolrow"
          href="/tools"
          onClick={(e) => {
            e.preventDefault()
          }}
        >
          🛡 管理页
          <span className="hint">成员账号 · 标签治理 · 备份 · 更新（#42 加入）</span>
        </a>
      </div>
    </div>
  )
}
