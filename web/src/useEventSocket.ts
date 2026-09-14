import { useCallback, useEffect, useRef, useState } from 'react'
import type { BotEvent } from './types'

export type ConnState = 'connecting' | 'open' | 'closed'

/**
 * Keeps a single WebSocket alive with auto-reconnect and forwards every
 * decoded event to the supplied handler.
 */
export function useEventSocket(onEvent: (e: BotEvent) => void) {
  const [state, setState] = useState<ConnState>('connecting')
  const handlerRef = useRef(onEvent)
  handlerRef.current = onEvent

  const retryRef = useRef(0)
  const timerRef = useRef<number | undefined>(undefined)
  const socketRef = useRef<WebSocket | null>(null)
  const closedRef = useRef(false)
  const connectedBeforeRef = useRef(false)

  const connect = useCallback(() => {
    if (closedRef.current) return

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${location.host}/ws`)
    socketRef.current = ws
    setState('connecting')

    ws.onopen = () => {
      if (closedRef.current || socketRef.current !== ws) {
        ws.close()
        return
      }
      // A restarted server has new in-memory job state and may embed a new
      // frontend. Reload the no-store shell rather than retaining stale UI.
      if (connectedBeforeRef.current) {
        window.location.reload()
        return
      }
      connectedBeforeRef.current = true
      retryRef.current = 0
      setState('open')
    }

    ws.onmessage = (ev) => {
      try {
        handlerRef.current(JSON.parse(ev.data) as BotEvent)
      } catch {
        // ignore malformed frames
      }
    }

    ws.onclose = () => {
      if (closedRef.current || socketRef.current !== ws) return
      setState('closed')
      if (closedRef.current) return
      // Exponential backoff capped at 8s.
      const delay = Math.min(8000, 500 * 2 ** retryRef.current)
      retryRef.current += 1
      timerRef.current = window.setTimeout(connect, delay)
    }

    ws.onerror = () => {
      try {
        ws.close()
      } catch {
        // ignore
      }
    }
  }, [])

  useEffect(() => {
    closedRef.current = false
    connect()
    return () => {
      closedRef.current = true
      if (timerRef.current) window.clearTimeout(timerRef.current)
      socketRef.current?.close()
    }
  }, [connect])

  return state
}
