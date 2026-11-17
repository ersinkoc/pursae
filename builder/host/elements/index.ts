/**
 * The element editor (builder/host/elements): installs the canvas and the navigator branch through
 * the builder's elements seam (patch 0020-elements-seam), and the inspector's Content, Style and
 * Advanced tabs for its elements (ContentPanel, StylePanel: local styles, classes, variables). A page made only of code sections is drawn
 * and edited by it; every other page keeps the builder's own canvas (and code sections in a
 * mixed page keep patches 0018 / 0019).
 */
import { setElementsSeam } from '@/builder/elementsSeam'
import type { DocState } from '@/builder/types'
import { ElementsCanvas } from './ElementsCanvas'
import { ElementsNav } from './ElementsNav'
import { StyleTab, styleTitle } from './StylePanel'
import { ContentTab } from './ContentPanel'

/** The element editor draws this doc: it has sections and every one is a code section. */
export const handlesDoc = (doc: DocState) => doc.sections.length > 0 && doc.sections.every((s) => s.kind === 'code')

export function installElements() {
  setElementsSeam({ handles: handlesDoc, Canvas: ElementsCanvas, Nav: ElementsNav, Tab: StyleTab, Content: ContentTab, title: styleTitle })
}
