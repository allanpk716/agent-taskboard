// 维度字段下拉框（#39）：可以按别名搜索，提供「不确定」选项。
// 候选值和别名随字段定义下发（字典端点仅成员可用，别随字典现取），
// 本地过滤：标准值或任一别名包含输入即命中。
import { useEffect, useRef, useState } from 'react'
import type { TemplateField } from './types'

export interface DimensionSelectProps {
  field: TemplateField
  value: string
  onChange: (value: string) => void
  /** 缺必填时的标红（边框由 .fld.miss 统一控制） */
  invalid?: boolean
}

/** 规范化比较：忽略大小写（机型值基本是 ASCII + 数字）。 */
function matches(option: string, aliases: string[] | undefined, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (q === '') return true
  if (option.toLowerCase().includes(q)) return true
  return (aliases ?? []).some((a) => a.toLowerCase().includes(q))
}

export function DimensionSelect({ field, value, onChange, invalid }: DimensionSelectProps) {
  const options = field.options ?? []
  const aliases = field.option_aliases ?? {}
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState<string | null>(null) // null = 显示当前选中值
  const rootRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)

  // 点击组件外面时收起下拉
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const shown = query ?? value
  const filtered = options.filter((o) => matches(o, aliases[o], shown))
  // 「不确定」始终可选（不知道准确型号也能提交，决议 6）。
  if (!filtered.includes('不确定')) filtered.push('不确定')

  function select(option: string) {
    onChange(option)
    setQuery(null)
    setOpen(false)
    inputRef.current?.blur()
  }

  return (
    <div className="dim-select" ref={rootRef}>
      <input
        ref={inputRef}
        role="combobox"
        aria-expanded={open}
        aria-controls="dim-listbox"
        aria-label={field.title}
        value={shown}
        placeholder="输入型号或俗称搜索，如 UD-2300"
        className={invalid ? 'invalid' : undefined}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => {
          setOpen(true)
          inputRef.current?.select() // 方便直接输入别名覆盖当前值
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            if (filtered.length > 0) select(filtered[0])
          } else if (e.key === 'Escape') {
            setOpen(false)
            setQuery(null)
          }
        }}
      />
      {open && (
        <ul className="dim-options" role="listbox" id="dim-listbox" aria-label={field.title}>
          {filtered.map((o) => (
            <li key={o} role="option" aria-selected={o === value} onMouseDown={(e) => e.preventDefault()} onClick={() => select(o)}>
              {o}
              {(aliases[o]?.length ?? 0) > 0 && (
                <span className="dim-alias" aria-hidden="true">
                  俗称：{aliases[o].join('、')}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
