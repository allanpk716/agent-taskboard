import { useEffect, useState } from 'react'

type ElementRef = { current: HTMLElement | null }

/**
 * 「↓ 最新回复」：页面只标出最后一条回复；这里统一负责观察它是否在
 * 视口（或传入的滚动容器）内、显隐按钮，以及平滑滚动到回复开头。
 */
export function JumpToLatestReply({
  targetRef,
  targetKey,
  scrollContainerRef,
  scrollContainerKey,
  scrollTopOffset = 0,
}: {
  targetRef: ElementRef
  /** 回复目标变化时强制重建 IntersectionObserver（ref.current 变化不触发 render）。 */
  targetKey?: number | string | null
  /** 有独立滚动容器时传入（例如宽屏成员详情的对话栏）。 */
  scrollContainerRef?: ElementRef
  scrollContainerKey?: string
  /** 视口滚动的目标顶部留白，用来避开吸顶/固定元素。 */
  scrollTopOffset?: number
}) {
  // null 表示还没有完成第一次相交判断，避免短对话打开时按钮闪一下。
  const [targetVisible, setTargetVisible] = useState<boolean | null>(null)

  useEffect(() => {
    const target = targetRef.current
    const root = scrollContainerRef?.current ?? null
    setTargetVisible(null)
    if (!target || !target.isConnected) return

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.at(-1)
        if (entry) setTargetVisible(entry.isIntersecting)
      },
      { root },
    )
    observer.observe(target)
    return () => observer.disconnect()
  }, [targetRef, targetKey, scrollContainerRef, scrollContainerKey])

  if (targetVisible !== false) return null

  function jump() {
    const target = targetRef.current
    if (!target) return
    const root = scrollContainerRef?.current
    const targetRect = target.getBoundingClientRect()
    if (root) {
      const rootRect = root.getBoundingClientRect()
      root.scrollTo({
        top: root.scrollTop + targetRect.top - rootRect.top - scrollTopOffset,
        behavior: 'smooth',
      })
      return
    }
    window.scrollTo({
      top: window.scrollY + targetRect.top - scrollTopOffset,
      behavior: 'smooth',
    })
  }

  return (
    <button type="button" className="jump-latest" aria-label="跳到最新回复" onClick={jump}>
      ↓ 最新回复
    </button>
  )
}
