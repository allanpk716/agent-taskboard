// 按字段分区的表单组件（#39）：补全说明与反馈入口提交页共用，#41 的成员
// 建单页也会复用。照 prototype/guest-web a.html 的表单样式：问句标题 +
// 必填红星 + 引导说明 + 灰色示例占位；文本字段可以粘贴或上传图片；维度
// 字段是可按别名搜索的下拉框（DimensionSelect）；单选字段是行内单选；
// 底部有统一的附件区。缺项标红（.fld.miss + .err 提示）由 missing 控制。
//
// 组件只管展示与输入回调：字段值、文件暂存/上传、校验都由调用方持有
//（便于各自接草稿和提交时机——补全说明立即上传拿 att: 引用，入口提交
// 前拿不到凭证，先暂存本地、建单后补传）。
import { useRef } from 'react'
import { DimensionSelect } from './DimensionSelect'
import type { TemplateField } from '../shared/types'
import { fmtSize } from '../shared/format'

/** 暂存的文件：补全说明场景立即上传（status 就绪时 markdown 里是 att: 引用）；入口场景提交后补传（blob 是文件本体）。 */
export interface StagedFile {
  id: string
  name: string
  size: number
  /** 引用插进哪个字段的值里（统一附件区 = 最后一个文本字段） */
  fieldKey: string
  /** 展示位置：true 列在统一附件区，false 列在所属字段下 */
  area: boolean
  status: 'uploading' | 'ready' | 'failed'
  error?: string
  /** 上传成功后现成的 Markdown 片段（![](att:N) 或 [名字](att:N)） */
  markdown?: string
  /** 图片的本地预览地址（objectURL，仅展示用） */
  previewUrl?: string
  /** 入口提交前拿不到凭证，先持有文件本体，建单后补传 */
  blob?: File
}

export interface FieldFormProps {
  fields: TemplateField[]
  values: Record<string, string>
  onChange: (key: string, value: string) => void
  /** 标红的缺项键（实时或提交被拦后由调用方给出） */
  missing?: ReadonlySet<string>
  /** 各缺项的自定义提示（默认「请填写这一项。」+ 引导说明） */
  hints?: Record<string, string>
  /** 文本字段里粘贴/选择图片、统一附件区选择文件时回调（压缩与上传由调用方做） */
  onPickFiles?: (fieldKey: string, files: File[], viaArea: boolean) => void
  /** 暂存文件列表（两处共用的统一附件区与字段下的附件条） */
  files?: StagedFile[]
  onRemoveFile?: (id: string) => void
}

/** 统一附件区默认把引用挂在最后一个文本字段后面（PATCH 只认字段值里的 att: 引用）。 */
export function lastTextFieldKey(fields: TemplateField[]): string | undefined {
  const texts = fields.filter((f) => f.type === 'text')
  return texts.length > 0 ? texts[texts.length - 1].key : undefined
}

/** 还没填的必填字段键（顺序同模板）。「不确定」和已有值都算已填。 */
export function missingRequired(fields: TemplateField[], values: Record<string, string>): string[] {
  return fields.filter((f) => f.required && !(values[f.key] ?? '').trim()).map((f) => f.key)
}

function fieldFiles(files: StagedFile[] | undefined, fieldKey: string, area: boolean): StagedFile[] {
  return (files ?? []).filter((f) => f.fieldKey === fieldKey && f.area === area)
}

function FileChips({ files, onRemove }: { files: StagedFile[]; onRemove?: (id: string) => void }) {
  if (files.length === 0) return null
  return (
    <div className="staged-files">
      {files.map((f) => (
        <span key={f.id} className={`staged-file ${f.status}`}>
          {f.previewUrl ? <img src={f.previewUrl} alt="" /> : '📄'}
          <span className="name">
            {f.name} · {fmtSize(f.size)}
          </span>
          {f.status === 'uploading' && <span className="state">上传中…</span>}
          {f.status === 'failed' && <span className="state">{f.error ?? '失败'}</span>}
          {onRemove && (
            <button type="button" className="rm" aria-label={`移除 ${f.name}`} onClick={() => onRemove(f.id)}>
              ×
            </button>
          )}
        </span>
      ))}
    </div>
  )
}

export function FieldForm(props: FieldFormProps) {
  const { fields, values, onChange, missing, hints, onPickFiles, files, onRemoveFile } = props
  const areaField = lastTextFieldKey(fields)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const pickTargetRef = useRef<{ fieldKey: string; viaArea: boolean }>({ fieldKey: '', viaArea: false })

  function openPicker(fieldKey: string, viaArea: boolean) {
    pickTargetRef.current = { fieldKey, viaArea }
    fileInputRef.current?.click()
  }

  return (
    <div className="form">
      {fields.map((f) => {
        const miss = missing?.has(f.key) ?? false
        const hint = miss ? (hints?.[f.key] ?? `请填写这一项。${f.guide}`) : null
        return (
          <div key={f.key} className={`fld${miss ? ' miss' : ''}`}>
            <label>
              {f.title}
              {f.required && <span className="req">必填</span>}
            </label>
            {f.guide && <div className="guide">{f.guide}</div>}
            {f.type === 'dimension' ? (
              <DimensionSelect field={f} value={values[f.key] ?? ''} onChange={(v) => onChange(f.key, v)} invalid={miss} />
            ) : f.type === 'radio' ? (
              <div className="radios">
                {(f.options ?? []).map((o) => (
                  <label key={o}>
                    <input
                      type="radio"
                      name={f.key}
                      value={o}
                      checked={values[f.key] === o}
                      onChange={() => onChange(f.key, o)}
                    />{' '}
                    {o}
                  </label>
                ))}
              </div>
            ) : (
              <>
                <textarea
                  placeholder={f.example ? `示例：${f.example}` : undefined}
                  value={values[f.key] ?? ''}
                  onChange={(e) => onChange(f.key, e.target.value)}
                  onPaste={(e) => {
                    // 文本字段可以直接粘贴截图（原型：文本字段可以贴图）
                    const imgs = Array.from(e.clipboardData?.files ?? []).filter((file) =>
                      file.type.startsWith('image/'),
                    )
                    if (imgs.length === 0 || !onPickFiles) return
                    e.preventDefault()
                    onPickFiles(f.key, imgs, false)
                  }}
                />
                {onPickFiles && (
                  <div className="tools">
                    <button type="button" className="linkish" onClick={() => openPicker(f.key, false)}>
                      🖼 粘贴或上传图片
                    </button>
                  </div>
                )}
                <FileChips files={fieldFiles(files, f.key, false)} onRemove={onRemoveFile} />
              </>
            )}
            {hint && <div className="err">{hint}</div>}
          </div>
        )
      })}
      <div className="fld">
        <label>附件</label>
        <div className="guide">照片、日志等文件；单个不超过 5 MB，超限请写 NAS 共享盘路径。</div>
        <label
          className="drop"
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault()
            const dropped = Array.from(e.dataTransfer?.files ?? [])
            if (dropped.length > 0 && areaField) onPickFiles?.(areaField, dropped, true)
          }}
        >
          拖到这里，或点击选择文件
          <input
            type="file"
            multiple
            hidden
            aria-label="选择附件"
            onChange={(e) => {
              const picked = Array.from(e.target.files ?? [])
              e.target.value = ''
              if (picked.length > 0 && areaField) onPickFiles?.(areaField, picked, true)
            }}
          />
        </label>
        <FileChips files={fieldFiles(files, areaField ?? '', true)} onRemove={onRemoveFile} />
      </div>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          const picked = Array.from(e.target.files ?? [])
          e.target.value = ''
          if (picked.length > 0) {
            const { fieldKey, viaArea } = pickTargetRef.current
            onPickFiles?.(fieldKey, picked, viaArea)
          }
        }}
      />
    </div>
  )
}

/** 把一段 Markdown 片段（att: 引用）追加到字段值末尾。 */
export function appendRef(value: string, markdown: string): string {
  const v = value ?? ''
  return v.trim() === '' ? markdown : `${v.replace(/\s+$/, '')}\n${markdown}`
}
