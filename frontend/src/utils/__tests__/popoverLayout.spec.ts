import { describe, expect, it } from 'vitest'
import { popoverLayout } from '../floatingPanel'

describe('selection popup viewport boundary', () => {
  it('reads native DOMRect prototype fields when positioning a real trigger', () => {
    expect(popoverLayout(new DOMRect(212, 40, 100, 30), {width:320,height:568})).toMatchObject({left:'112px',top:'74px'})
  })
  it('keeps the full readable panel inside a narrow viewport at the right edge', () => {
    expect(popoverLayout({left: 300, top: 40, bottom: 80, width: 100}, {width: 320, height: 568}, 380)).toMatchObject({left:'8px',width:'304px',top:'84px',maxHeight:'476px'})
  })
  it('opens above a low trigger and bounds its scroll region', () => {
    expect(popoverLayout({left: 100, top: 250, bottom: 290, width: 80}, {width: 640, height: 320}, 200)).toMatchObject({left:'100px',bottom:'74px',maxHeight:'238px'})
  })
  it('follows a trigger wider than the usual popup while retaining viewport margins', () => {
    expect(popoverLayout({left: 12, top: 30, bottom: 70, width: 500}, {width: 390, height: 640}, 200)).toMatchObject({left:'8px',width:'374px',maxWidth:'374px'})
  })
})
