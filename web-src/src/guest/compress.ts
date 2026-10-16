// 上传前压缩图片（spec「附件显示与安全」）：长边缩到 2048px、转为 JPEG（质量 85%）、
// 去掉 EXIF（canvas 重编码天然不带 EXIF）；本来就不到 1MB 的 PNG 保持原样。
// 服务端只检查上限、不压缩（Agent 直传不压缩）。
const MAX_LONG_EDGE = 2048
const SMALL_PNG_LIMIT = 1024 * 1024

export async function compressImage(file: File): Promise<File> {
  if (file.type === 'image/png' && file.size < SMALL_PNG_LIMIT) return file
  try {
    const bmp = await createImageBitmap(file)
    const long = Math.max(bmp.width, bmp.height)
    const scale = long > MAX_LONG_EDGE ? MAX_LONG_EDGE / long : 1
    const w = Math.max(1, Math.round(bmp.width * scale))
    const h = Math.max(1, Math.round(bmp.height * scale))
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) return file
    ctx.drawImage(bmp, 0, 0, w, h)
    bmp.close?.()
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.85))
    if (!blob || blob.size === 0) return file
    const name = /\.(jpe?g)$/i.test(file.name) ? file.name : `${file.name.replace(/\.[^.]+$/, '')}.jpg`
    return new File([blob], name, { type: 'image/jpeg', lastModified: Date.now() })
  } catch {
    // 解码失败（损坏的图片等）就原样上传，由服务端按上限处理。
    return file
  }
}
