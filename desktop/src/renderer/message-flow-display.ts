export type MessageFlowLocale = 'en' | 'zh'

export type MessageFlowStatusInput = {
  done: boolean
  failed: boolean
  hasFinalText: boolean
  finalizing?: boolean
  stalled?: boolean
  locale?: MessageFlowLocale
}

export type MessageFlowCommandInput = {
  name?: string
  input?: unknown
  subject?: string
  label?: string
}

export type MessageFlowPartRole = 'text' | 'process' | 'ignore'

export function messageFlowFinalTextIndex<T>(
  parts: readonly T[],
  roleForPart: (part: T, index: number) => MessageFlowPartRole,
): number {
  let finalTextIndex = -1
  let lastProcessIndex = -1

  parts.forEach((part, index) => {
    const role = roleForPart(part, index)
    if (role === 'process') {
      lastProcessIndex = index
      return
    }
    if (role === 'text') {
      finalTextIndex = index
    }
  })

  return finalTextIndex > lastProcessIndex ? finalTextIndex : -1
}

export function messageFlowStatusLabel({
  done,
  failed,
  hasFinalText,
  finalizing = false,
  stalled = false,
  locale = 'en',
}: MessageFlowStatusInput): string {
  const appLocale = locale === 'zh' ? 'zh-CN' : 'en-US'
  if (done) {
    return translate(appLocale, failed ? 'messageFlow.activityFailed' : 'messageFlow.activityLog')
  }
  if (stalled) {
    return translate(appLocale, 'messageFlow.stillGenerating')
  }
  if (finalizing) {
    return translate(appLocale, 'messageFlow.finalizing')
  }
  return translate(appLocale, hasFinalText ? 'messageFlow.replying' : 'messageFlow.working')
}

export function isMessageFlowFailedStatus(status: string | undefined): boolean {
  return status === 'failed' || status === 'error'
}

export function formatMessageFlowCommand({
  name,
  input,
  subject,
  label,
}: MessageFlowCommandInput): string {
  const toolName = name?.trim() || 'tool'
  const formattedInput = formatUnknownInput(input)
  if (formattedInput) {
    return `${toolName} ${formattedInput}`
  }
  if (subject?.trim()) {
    return `${toolName} ${subject.trim()}`
  }
  if (label?.trim() && label.trim() !== toolName) {
    return `${toolName} ${label.trim()}`
  }
  return toolName
}

function formatUnknownInput(value: unknown): string {
  if (value == null) {
    return ''
  }
  if (typeof value === 'string') {
    return value.trim()
  }
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}
import { translate } from "./i18n";
