export interface FloatingPanelPosition {
  top: number | null
  bottom: number | null
  left: number
  width: number
  maxHeight: number
}

export interface FloatingPanelOptions {
  alignment?: 'start' | 'end'
  viewportPadding?: number
  gap?: number
  maxWidth?: number
  maxHeightRatio?: number
  mobileBreakpoint?: number
  minComfortableHeight?: number
}

/**
 * 计算挂载到 body 的浮层位置，避免触发按钮靠近视口边缘时浮层被挤到屏幕外。
 */
export const getFloatingPanelPosition = (
  triggerRect: Pick<DOMRect, 'top' | 'right' | 'bottom'> & Partial<Pick<DOMRect, 'left'>>,
  viewportWidth: number,
  viewportHeight: number,
  options: FloatingPanelOptions = {}
): FloatingPanelPosition => {
  const viewportPadding = options.viewportPadding ?? 16
  const gap = options.gap ?? 8
  const maxWidth = options.maxWidth ?? 320
  const maxHeightRatio = options.maxHeightRatio ?? 0.7
  const mobileBreakpoint = options.mobileBreakpoint ?? 768
  const minComfortableHeight = options.minComfortableHeight ?? 240

  const availableWidth = Math.max(0, viewportWidth - viewportPadding * 2)
  const width = Math.min(maxWidth, availableWidth)
  const anchoredLeft = options.alignment === 'start'
    ? triggerRect.left ?? triggerRect.right - width
    : triggerRect.right - width
  const left = options.alignment !== 'start' && viewportWidth < mobileBreakpoint
    ? viewportPadding
    : Math.max(viewportPadding, Math.min(anchoredLeft, viewportWidth - width - viewportPadding))

  const preferredMaxHeight = Math.max(0, Math.floor(viewportHeight * maxHeightRatio))
  const anchorTop = Math.max(viewportPadding, Math.min(triggerRect.top, viewportHeight - viewportPadding))
  const anchorBottom = Math.max(viewportPadding, Math.min(triggerRect.bottom, viewportHeight - viewportPadding))
  const spaceBelow = Math.max(0, viewportHeight - anchorBottom - gap - viewportPadding)
  const spaceAbove = Math.max(0, anchorTop - gap - viewportPadding)
  const openAbove = spaceBelow < Math.min(minComfortableHeight, preferredMaxHeight) && spaceAbove > spaceBelow
  const maxHeight = Math.min(preferredMaxHeight, openAbove ? spaceAbove : spaceBelow)

  return {
    top: openAbove ? null : anchorBottom + gap,
    bottom: openAbove ? viewportHeight - anchorTop + gap : null,
    left,
    width,
    maxHeight
  }
}

/** CSS adapter for selection menus, sharing the same viewport boundary calculation. */
export function popoverLayout(rect: Pick<DOMRect, 'left' | 'top' | 'bottom' | 'width'>, viewport: { width: number; height: number }, preferredWidth = 200, preferredHeight = 372) {
  const position = getFloatingPanelPosition(
    { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.left + rect.width }, viewport.width, viewport.height,
    { alignment: 'start', viewportPadding: 8, gap: 4, maxWidth: Math.max(preferredWidth, rect.width), maxHeightRatio: 1, minComfortableHeight: preferredHeight }
  )
  return {
    position: 'fixed' as const,
    left: `${position.left}px`,
    width: `${position.width}px`,
    maxWidth: `${Math.max(0, viewport.width - 16)}px`,
    maxHeight: `${position.maxHeight}px`,
    ...(position.top !== null ? { top: `${position.top}px` } : { bottom: `${position.bottom}px` })
  }
}
