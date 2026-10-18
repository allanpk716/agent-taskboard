import { useEffect, useState } from 'react'

export type LayoutTier = 'wide' | 'medium' | 'narrow' | 'mobile'

function tierFor(width: number): LayoutTier {
  if (width >= 1280) return 'wide'
  if (width >= 1024) return 'medium'
  if (width >= 768) return 'narrow'
  return 'mobile'
}

/** 成员端唯一的布局档位来源；页面不要各自判断窗口宽度。 */
export function useLayoutTier(): LayoutTier {
  const [tier, setTier] = useState<LayoutTier>(() => tierFor(window.innerWidth))
  useEffect(() => {
    const update = () => setTier(tierFor(window.innerWidth))
    update()
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [])
  return tier
}
