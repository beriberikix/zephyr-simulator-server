import { useParams, useNavigate } from 'react-router-dom'
import { useState, useEffect, useRef } from 'react'
import axios from 'axios'
import NetworkConfigPanel from '../components/NetworkConfigPanel'
import { useAuth } from '../context/AuthContext'
import { SessionDetail } from '../types/session'

interface DebugTarget {
  enabled: boolean
  host: string
  port: number
  state: string
  container?: string
}

interface Breakpoint {
  number: string
  location: string
  enabled: boolean
}

interface StackFrame {
  index: number
  function: string
  location: string
}

interface SanitizerFinding {
  tool: string
  file?: string
  line?: number
  column?: number
  summary: string
  source: string
  raw: string
}

interface SanitizerReport {
  session_id: string
  total: number
  by_tool: Record<string, number>
  findings: SanitizerFinding[]
  filters: Record<string, string>
  generated_at: string
}

type DebugConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error'

export default function EmulatorPage() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const navigate = useNavigate()
  const { isAuthenticated } = useAuth()
  const [session, setSession] = useState<SessionDetail | null>(null)
  const [terminalOutput, setTerminalOutput] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [message, setMessage] = useState<string>('')
  const [refreshKey, setRefreshKey] = useState(0)
  const [debugTarget, setDebugTarget] = useState<DebugTarget | null>(null)
  const [debugLoading, setDebugLoading] = useState(false)
  const [debugState, setDebugState] = useState<DebugConnectionState>('disconnected')
  const [debugError, setDebugError] = useState('')
  const [debugStream, setDebugStream] = useState<string[]>([])
  const [breakpoints, setBreakpoints] = useState<Breakpoint[]>([])
  const [stackFrames, setStackFrames] = useState<StackFrame[]>([])
  const [breakpointInput, setBreakpointInput] = useState('main')
  const [debugActionLoading, setDebugActionLoading] = useState<string | null>(null)
  const [debuggerExpanded, setDebuggerExpanded] = useState(false)
  const [networkToolsExpanded, setNetworkToolsExpanded] = useState(false)
  const [analysisToolsExpanded, setAnalysisToolsExpanded] = useState(false)
  const [sanitizerReport, setSanitizerReport] = useState<SanitizerReport | null>(null)
  const [sanitizerReportLoading, setSanitizerReportLoading] = useState(false)
  const [sanitizerToolFilter, setSanitizerToolFilter] = useState('')
  const [sanitizerSearch, setSanitizerSearch] = useState('')
  const debugSocketRef = useRef<WebSocket | null>(null)
  const manualDebugCloseRef = useRef(false)

  useEffect(() => {
    const fetchSession = async () => {
      try {
        const response = await axios.get(`/api/sessions/${sessionId}`)
        setSession(response.data.data)
      } catch (err) {
        console.error('Failed to fetch session:', err)
        navigate('/sessions')
      } finally {
        setLoading(false)
      }
    }

    fetchSession()
  }, [sessionId, navigate, refreshKey])

  useEffect(() => {
    const fetchDebugTarget = async () => {
      if (!sessionId) return
      setDebugLoading(true)
      try {
        const response = await axios.get(`/api/sessions/${sessionId}/debug-target`)
        setDebugTarget(response.data.data)
        setDebugError('')
      } catch (err: any) {
        const errorMessage = err?.response?.data?.error || 'Failed to fetch debug target'
        setDebugError(errorMessage)
        setDebugTarget(null)
      } finally {
        setDebugLoading(false)
      }
    }

    fetchDebugTarget()
  }, [sessionId, refreshKey])

  useEffect(() => {
    if (!sessionId) return
    if (!session?.asan_enabled && !session?.ubsan_enabled) {
      setSanitizerReport(null)
      return
    }
    void fetchSanitizerReport(sanitizerToolFilter, sanitizerSearch)
  }, [sessionId, refreshKey, session?.asan_enabled, session?.ubsan_enabled])

  useEffect(() => {
    if (!message) return
    const timer = window.setTimeout(() => setMessage(''), 6000)
    return () => window.clearTimeout(timer)
  }, [message])

  useEffect(() => {
    if (!session) return

    // Set up SSE connection for real-time updates
    const eventSource = new EventSource(`/api/sse?session=${sessionId}`)

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        setTerminalOutput((prev) => [...prev, data.data])
      } catch (err) {
        console.error('Failed to parse SSE event:', err)
      }
    }

    eventSource.onerror = () => {
      eventSource.close()
    }

    return () => eventSource.close()
  }, [session, sessionId])

  useEffect(() => {
    return () => {
      manualDebugCloseRef.current = true
      if (debugSocketRef.current) {
        debugSocketRef.current.close()
        debugSocketRef.current = null
      }
    }
  }, [])

  const runAction = async (action: 'start' | 'stop' | 'pause' | 'resume') => {
    if (!sessionId) return

    setActionLoading(action)
    setMessage('')
    try {
      const response = await axios.post(`/api/sessions/${sessionId}/${action}`)
      setSession(response.data.data)
      setMessage(`Session ${action} successful`)
      setRefreshKey((prev) => prev + 1)

      if (action === 'stop' || action === 'pause') {
        manualDebugCloseRef.current = true
        if (debugSocketRef.current) {
          debugSocketRef.current.close()
          debugSocketRef.current = null
        }
        setDebugState('disconnected')
      }
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || `Failed to ${action} session`
      setMessage(errorMessage)
    } finally {
      setActionLoading(null)
    }
  }

  const toggleCoverage = async (enabled: boolean) => {
    if (!sessionId) return
    setActionLoading('coverage')
    setMessage('')
    try {
      const response = await axios.patch(`/api/sessions/${sessionId}`, { coverage_enabled: enabled })
      setSession(response.data.data)
      setMessage(`Coverage ${enabled ? 'enabled' : 'disabled'} successfully`)
      setRefreshKey((prev) => prev + 1)
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to update coverage setting'
      setMessage(errorMessage)
    } finally {
      setActionLoading(null)
    }
  }

  const toggleSanitizer = async (kind: 'asan' | 'ubsan', enabled: boolean) => {
    if (!sessionId) return
    setActionLoading(kind)
    setMessage('')
    try {
      const payload = kind === 'asan' ? { asan_enabled: enabled } : { ubsan_enabled: enabled }
      const response = await axios.patch(`/api/sessions/${sessionId}`, payload)
      setSession(response.data.data)
      setMessage(`${kind.toUpperCase()} ${enabled ? 'enabled' : 'disabled'} successfully`)
      setRefreshKey((prev) => prev + 1)
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || `Failed to update ${kind.toUpperCase()} setting`
      setMessage(errorMessage)
    } finally {
      setActionLoading(null)
    }
  }

  const togglePCAP = async (enabled: boolean) => {
    if (!sessionId) return
    setActionLoading('pcap')
    setMessage('')
    try {
      const response = await axios.patch(`/api/sessions/${sessionId}`, { pcap_enabled: enabled })
      setSession(response.data.data)
      setMessage(`PCAP ${enabled ? 'enabled' : 'disabled'} successfully`)
      setRefreshKey((prev) => prev + 1)
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to update PCAP setting'
      setMessage(errorMessage)
    } finally {
      setActionLoading(null)
    }
  }

  const fetchSanitizerReport = async (toolFilter: string, query: string) => {
    if (!sessionId) return

    setSanitizerReportLoading(true)
    try {
      const params = new URLSearchParams()
      if (toolFilter) params.set('tool', toolFilter)
      if (query.trim()) params.set('q', query.trim())
      params.set('limit', '200')

      const qs = params.toString()
      const response = await axios.get(`/api/sessions/${sessionId}/sanitizers/report${qs ? `?${qs}` : ''}`)
      setSanitizerReport(response.data?.data || null)
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to load sanitizer findings'
      setMessage(errorMessage)
      setSanitizerReport(null)
    } finally {
      setSanitizerReportLoading(false)
    }
  }

  const connectDebug = () => {
    if (!sessionId) return
    if (!debugTarget?.enabled) {
      setDebugError('Enable debug_config for this session before connecting.')
      return
    }
    if (session?.state !== 'running') {
      setDebugError('Session must be running to open debugger tunnel.')
      return
    }

    manualDebugCloseRef.current = false
    setDebugError('')
    setDebugState('connecting')

    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${wsProtocol}//${window.location.host}/api/sessions/${sessionId}/debug/ws`
    const ws = new WebSocket(wsUrl)
    debugSocketRef.current = ws

    ws.onopen = () => {
      setDebugState('connected')
      setDebugStream((prev) => [...prev, '[debug] connected'])
    }

    ws.onmessage = async (event) => {
      if (typeof event.data === 'string') {
        setDebugStream((prev) => [...prev, event.data])
        return
      }
      if (event.data instanceof Blob) {
        const text = await event.data.text()
        setDebugStream((prev) => [...prev, text])
        return
      }
      if (event.data instanceof ArrayBuffer) {
        const text = new TextDecoder().decode(event.data)
        setDebugStream((prev) => [...prev, text])
      }
    }

    ws.onerror = () => {
      setDebugState('error')
      setDebugError('Debugger websocket error.')
    }

    ws.onclose = () => {
      debugSocketRef.current = null
      setDebugState('disconnected')
      if (!manualDebugCloseRef.current) {
        setDebugError('Debugger websocket closed by server.')
      }
    }
  }

  const disconnectDebug = () => {
    manualDebugCloseRef.current = true
    if (debugSocketRef.current) {
      debugSocketRef.current.close()
      debugSocketRef.current = null
    }
    setDebugState('disconnected')
  }

  const refreshBreakpoints = async () => {
    if (!sessionId) return
    setDebugActionLoading('list-breakpoints')
    try {
      const response = await axios.get(`/api/sessions/${sessionId}/debug/breakpoints`)
      setBreakpoints(response.data?.data?.breakpoints || [])
      setDebugError('')
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to load breakpoints'
      setDebugError(errorMessage)
    } finally {
      setDebugActionLoading(null)
    }
  }

  const addBreakpoint = async () => {
    if (!sessionId) return
    if (!breakpointInput.trim()) {
      setDebugError('Breakpoint location is required')
      return
    }

    setDebugActionLoading('add-breakpoint')
    try {
      const response = await axios.post(`/api/sessions/${sessionId}/debug/breakpoints`, {
        location: breakpointInput.trim(),
      })
      setBreakpoints(response.data?.data?.breakpoints || [])
      setDebugError('')
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to add breakpoint'
      setDebugError(errorMessage)
    } finally {
      setDebugActionLoading(null)
    }
  }

  const removeBreakpoint = async (number: string) => {
    if (!sessionId) return
    setDebugActionLoading(`delete-breakpoint-${number}`)
    try {
      const response = await axios.delete(`/api/sessions/${sessionId}/debug/breakpoints/${number}`)
      setBreakpoints(response.data?.data?.breakpoints || [])
      setDebugError('')
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to remove breakpoint'
      setDebugError(errorMessage)
    } finally {
      setDebugActionLoading(null)
    }
  }

  const refreshStackTrace = async () => {
    if (!sessionId) return
    setDebugActionLoading('stack-trace')
    try {
      const response = await axios.get(`/api/sessions/${sessionId}/debug/stack`)
      setStackFrames(response.data?.data?.frames || [])
      setDebugError('')
    } catch (err: any) {
      const errorMessage = err?.response?.data?.error || 'Failed to load stack trace'
      setDebugError(errorMessage)
    } finally {
      setDebugActionLoading(null)
    }
  }

  if (loading) {
    return <div className="text-center py-12 text-slate-700 dark:text-slate-300">Loading...</div>
  }

  if (!session) {
    return <div className="text-center py-12 text-slate-700 dark:text-slate-300">Session not found</div>
  }

  return (
    <div>
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100 mb-2">Emulator Session</h1>
        <p className="text-slate-700 dark:text-slate-300">ID: {sessionId}</p>
        {message && (
          <div className="mt-3 rounded-md bg-sky-50 dark:bg-sky-900/40 border border-sky-200 dark:border-sky-700 px-3 py-2 text-sm text-sky-900 dark:text-sky-200 flex items-start justify-between gap-3">
            <span>{message}</span>
            <button
              type="button"
              onClick={() => setMessage('')}
              className="text-sky-800 dark:text-sky-200 hover:text-sky-950 dark:hover:text-sky-100 font-semibold leading-none"
              aria-label="Dismiss message"
            >
              x
            </button>
          </div>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        {/* Control Panel */}
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100 mb-4">Control Panel</h2>

          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-slate-700 dark:text-slate-200 mb-1">
                Status
              </label>
              <div className="flex items-center gap-2">
                <div className={`w-3 h-3 rounded-full ${
                  session.state === 'running' ? 'bg-emerald-500' :
                  session.state === 'paused' ? 'bg-amber-500' :
                  'bg-rose-500'
                }`}></div>
                <span className="text-sm font-medium text-slate-900 dark:text-slate-100 capitalize">
                  {session.state}
                </span>
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-slate-700 dark:text-slate-200 mb-1">
                Seed
              </label>
              <p className="text-sm text-slate-700 dark:text-slate-300">{session.seed}</p>
            </div>

            <div className="border-t pt-4">
              <button
                onClick={() => runAction('start')}
                disabled={actionLoading !== null || session.state === 'running'}
                className="w-full bg-emerald-700 dark:bg-emerald-600 text-white py-2 px-4 rounded-lg hover:bg-emerald-800 dark:hover:bg-emerald-500 disabled:bg-slate-400 disabled:cursor-not-allowed mb-2"
              >
                Start
              </button>
              <button
                onClick={() => runAction('pause')}
                disabled={actionLoading !== null || session.state !== 'running'}
                className="w-full bg-amber-700 dark:bg-amber-600 text-white py-2 px-4 rounded-lg hover:bg-amber-800 dark:hover:bg-amber-500 disabled:bg-slate-400 disabled:cursor-not-allowed mb-2"
              >
                Pause
              </button>
              <button
                onClick={() => runAction('resume')}
                disabled={actionLoading !== null || session.state !== 'paused'}
                className="w-full bg-indigo-700 dark:bg-indigo-600 text-white py-2 px-4 rounded-lg hover:bg-indigo-800 dark:hover:bg-indigo-500 disabled:bg-slate-400 disabled:cursor-not-allowed mb-2"
              >
                Resume
              </button>
              <button
                onClick={() => runAction('stop')}
                disabled={actionLoading !== null || session.state === 'stopped'}
                className="w-full bg-rose-700 dark:bg-rose-600 text-white py-2 px-4 rounded-lg hover:bg-rose-800 dark:hover:bg-rose-500 disabled:bg-slate-400 disabled:cursor-not-allowed"
              >
                Stop
              </button>
            </div>

            <div className="border-t pt-4">
              <button
                onClick={() => setNetworkToolsExpanded((prev) => !prev)}
                className="w-full flex items-center justify-between rounded-lg border border-slate-200 dark:border-slate-700 px-3 py-2 text-sm font-medium text-slate-800 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-slate-800"
              >
                <span>Network Tools</span>
                <span>{networkToolsExpanded ? 'Hide' : 'Show'}</span>
              </button>

              {networkToolsExpanded && (
                <div className="mt-3">
                  <NetworkConfigPanel
                    sessionId={sessionId || ''}
                    onConfigUpdate={() => setRefreshKey(prev => prev + 1)}
                    embedded
                  />
                </div>
              )}
            </div>

            <div className="border-t pt-4">
              <button
                onClick={() => setAnalysisToolsExpanded((prev) => !prev)}
                className="w-full flex items-center justify-between rounded-lg border border-slate-200 dark:border-slate-700 px-3 py-2 text-sm font-medium text-slate-800 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-slate-800"
              >
                <span>Coverage & Sanitizers</span>
                <span>{analysisToolsExpanded ? 'Hide' : 'Show'}</span>
              </button>

              {analysisToolsExpanded && (
                isAuthenticated ? (
                  <div className="mt-3 space-y-4">
                    {/* Coverage Controls */}
                    <div className="space-y-2">
                      <div className="flex gap-2">
                        <button
                          onClick={() => toggleCoverage(true)}
                          disabled={actionLoading !== null || !!session.coverage_enabled}
                          className="flex-1 bg-emerald-700 dark:bg-emerald-600 text-white py-2 px-3 rounded-lg hover:bg-emerald-800 dark:hover:bg-emerald-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Enable Coverage
                        </button>
                        <button
                          onClick={() => toggleCoverage(false)}
                          disabled={actionLoading !== null || !session.coverage_enabled}
                          className="flex-1 bg-slate-700 dark:bg-slate-600 text-white py-2 px-3 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Disable Coverage
                        </button>
                      </div>
                      {session.coverage_enabled && (
                        <a
                          href={`/api/sessions/${sessionId}/coverage`}
                          download
                          className="w-full block text-center bg-teal-700 dark:bg-teal-600 text-white py-2 px-3 rounded-lg hover:bg-teal-800 dark:hover:bg-teal-500 text-sm"
                        >
                          Download Coverage (.tar.gz)
                        </a>
                      )}
                    </div>

                    {/* Sanitizer Controls */}
                    <div className="space-y-2">
                      <div className="grid grid-cols-2 gap-2">
                        <button
                          onClick={() => toggleSanitizer('asan', true)}
                          disabled={actionLoading !== null || !!session.asan_enabled}
                          className="bg-amber-700 dark:bg-amber-600 text-white py-2 px-3 rounded-lg hover:bg-amber-800 dark:hover:bg-amber-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Enable ASan
                        </button>
                        <button
                          onClick={() => toggleSanitizer('asan', false)}
                          disabled={actionLoading !== null || !session.asan_enabled}
                          className="bg-slate-700 dark:bg-slate-600 text-white py-2 px-3 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Disable ASan
                        </button>
                        <button
                          onClick={() => toggleSanitizer('ubsan', true)}
                          disabled={actionLoading !== null || !!session.ubsan_enabled}
                          className="bg-orange-700 dark:bg-orange-600 text-white py-2 px-3 rounded-lg hover:bg-orange-800 dark:hover:bg-orange-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Enable UBSan
                        </button>
                        <button
                          onClick={() => toggleSanitizer('ubsan', false)}
                          disabled={actionLoading !== null || !session.ubsan_enabled}
                          className="bg-slate-700 dark:bg-slate-600 text-white py-2 px-3 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Disable UBSan
                        </button>
                      </div>
                      {(session.asan_enabled || session.ubsan_enabled) && (
                        <a
                          href={`/api/sessions/${sessionId}/sanitizers`}
                          download
                          className="w-full block text-center bg-orange-700 dark:bg-orange-600 text-white py-2 px-3 rounded-lg hover:bg-orange-800 dark:hover:bg-orange-500 text-sm"
                        >
                          Download Sanitizer Reports (.tar.gz)
                        </a>
                      )}
                    </div>
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-amber-900 dark:text-amber-200 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-700 rounded px-3 py-2">
                    <a href="/login" className="font-medium underline">Sign in</a> to access coverage and sanitizer tools.
                  </p>
                )
              )}
            </div>

            {/* Remote debugger controls */}
            <div className="border-t pt-4">
              <button
                onClick={() => setDebuggerExpanded((prev) => !prev)}
                className="w-full flex items-center justify-between rounded-lg border border-slate-200 dark:border-slate-700 px-3 py-2 text-sm font-medium text-slate-800 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-slate-800"
              >
                <span>Debugger Tools</span>
                <span>{debuggerExpanded ? 'Hide' : 'Show'}</span>
              </button>

              {debuggerExpanded && (
                isAuthenticated ? (
                  <div className="mt-3">
                    <div className="flex items-center justify-between mb-2">
                      <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">GDB Remote Debug</h3>
                      <span className={`text-xs px-2 py-1 rounded-full ${
                        debugState === 'connected' ? 'bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200' :
                        debugState === 'connecting' ? 'bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200' :
                        debugState === 'error' ? 'bg-rose-100 text-rose-900 dark:bg-rose-900/40 dark:text-rose-200' :
                        'bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-100'
                      }`}>
                        {debugState}
                      </span>
                    </div>
                    {debugLoading ? (
                      <p className="text-xs text-slate-600 dark:text-slate-300 mb-2">Loading debug target...</p>
                    ) : debugTarget ? (
                      <div className="text-xs text-slate-700 dark:text-slate-300 mb-2 space-y-1">
                        <p>Enabled: {debugTarget.enabled ? 'yes' : 'no'}</p>
                        <p>Target: {debugTarget.host}:{debugTarget.port}</p>
                        <p>State: {debugTarget.state}</p>
                      </div>
                    ) : (
                      <p className="text-xs text-slate-600 dark:text-slate-300 mb-2">No debug target available.</p>
                    )}

                    <div className="flex gap-2">
                      <button
                        onClick={connectDebug}
                        disabled={debugState === 'connecting' || debugState === 'connected'}
                        className="flex-1 bg-sky-700 dark:bg-sky-600 text-white py-2 px-3 rounded-lg hover:bg-sky-800 dark:hover:bg-sky-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                      >
                        Connect
                      </button>
                      <button
                        onClick={disconnectDebug}
                        disabled={debugState !== 'connected'}
                        className="flex-1 bg-slate-700 dark:bg-slate-600 text-white py-2 px-3 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                      >
                        Disconnect
                      </button>
                    </div>

                    <div className="mt-3 border-t border-slate-200 dark:border-slate-700 pt-3 space-y-2">
                      <h4 className="text-sm font-semibold text-slate-900 dark:text-slate-100">PCAP Capture</h4>
                      <div className="grid grid-cols-2 gap-2">
                        <button
                          onClick={() => togglePCAP(true)}
                          disabled={actionLoading !== null || !!session.pcap_enabled}
                          className="bg-violet-700 dark:bg-violet-600 text-white py-2 px-3 rounded-lg hover:bg-violet-800 dark:hover:bg-violet-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Enable PCAP
                        </button>
                        <button
                          onClick={() => togglePCAP(false)}
                          disabled={actionLoading !== null || !session.pcap_enabled}
                          className="bg-slate-700 dark:bg-slate-600 text-white py-2 px-3 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                        >
                          Disable PCAP
                        </button>
                      </div>
                      {session.pcap_enabled && session.pcap_file_path && (
                        <a
                          href={`/api/sessions/${sessionId}/pcap`}
                          download
                          className="w-full block text-center bg-violet-700 dark:bg-violet-600 text-white py-2 px-3 rounded-lg hover:bg-violet-800 dark:hover:bg-violet-500 text-sm"
                        >
                          Download PCAP
                        </a>
                      )}
                    </div>

                    {debugError && (
                      <p className="mt-2 text-xs text-rose-900 dark:text-rose-200 bg-rose-50 dark:bg-rose-900/40 border border-rose-200 dark:border-rose-700 rounded px-2 py-1">
                        {debugError}
                      </p>
                    )}
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-amber-900 dark:text-amber-200 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-700 rounded px-3 py-2">
                    <a href="/login" className="font-medium underline">Sign in</a> to access GDB debugger and PCAP capture.
                  </p>
                )
              )}
            </div>
          </div>
        </div>

        {/* Networking & Terminal */}
        <div className="lg:col-span-2 space-y-6">
          {(session.asan_enabled || session.ubsan_enabled) && (
            <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden">
              <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700 bg-orange-50 dark:bg-orange-900/20 flex flex-wrap gap-3 items-center justify-between">
                <h2 className="text-lg font-semibold text-orange-900 dark:text-orange-200">Sanitizer Findings</h2>
                <div className="text-xs text-orange-800 dark:text-orange-200">
                  Total: {sanitizerReport?.total || 0}
                </div>
              </div>
              <div className="p-4 space-y-3">
                <div className="flex flex-wrap gap-2">
                  <select
                    value={sanitizerToolFilter}
                    onChange={(e) => setSanitizerToolFilter(e.target.value)}
                    className="px-3 py-2 border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 text-slate-900 dark:text-slate-100 rounded-lg text-sm"
                  >
                    <option value="">All tools</option>
                    <option value="asan">ASan</option>
                    <option value="ubsan">UBSan</option>
                  </select>
                  <input
                    type="text"
                    placeholder="Search findings"
                    value={sanitizerSearch}
                    onChange={(e) => setSanitizerSearch(e.target.value)}
                    className="flex-1 min-w-48 px-3 py-2 border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 text-slate-900 dark:text-slate-100 rounded-lg text-sm"
                  />
                  <button
                    onClick={() => fetchSanitizerReport(sanitizerToolFilter, sanitizerSearch)}
                    disabled={sanitizerReportLoading}
                    className="bg-orange-700 dark:bg-orange-600 text-white px-3 py-2 rounded-lg hover:bg-orange-800 dark:hover:bg-orange-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                  >
                    {sanitizerReportLoading ? 'Loading...' : 'Refresh'}
                  </button>
                </div>

                <div className="text-xs text-slate-700 dark:text-slate-300 flex gap-4">
                  <span>ASan: {sanitizerReport?.by_tool?.asan || 0}</span>
                  <span>UBSan: {sanitizerReport?.by_tool?.ubsan || 0}</span>
                </div>

                {sanitizerReportLoading ? (
                  <p className="text-sm text-slate-600 dark:text-slate-300">Parsing sanitizer logs...</p>
                ) : sanitizerReport && sanitizerReport.findings.length > 0 ? (
                  <div className="max-h-72 overflow-auto border border-slate-200 dark:border-slate-700 rounded-lg">
                    <table className="min-w-full text-sm">
                      <thead className="bg-slate-100 dark:bg-slate-800 sticky top-0">
                        <tr>
                          <th className="px-3 py-2 text-left text-xs font-medium text-slate-700 dark:text-slate-200 uppercase">Tool</th>
                          <th className="px-3 py-2 text-left text-xs font-medium text-slate-700 dark:text-slate-200 uppercase">Location</th>
                          <th className="px-3 py-2 text-left text-xs font-medium text-slate-700 dark:text-slate-200 uppercase">Summary</th>
                        </tr>
                      </thead>
                      <tbody>
                        {sanitizerReport.findings.map((finding, idx) => (
                          <tr key={`${finding.tool}-${finding.source}-${idx}`} className="border-t border-slate-100 dark:border-slate-700 align-top">
                            <td className="px-3 py-2">
                              <span className={`inline-block px-2 py-1 rounded text-xs font-medium ${finding.tool === 'asan' ? 'bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200' : 'bg-orange-100 text-orange-900 dark:bg-orange-900/40 dark:text-orange-200'}`}>
                                {finding.tool.toUpperCase()}
                              </span>
                            </td>
                            <td className="px-3 py-2 text-xs text-slate-700 dark:text-slate-300">
                              {finding.file ? `${finding.file}:${finding.line || 0}${finding.column ? `:${finding.column}` : ''}` : finding.source}
                            </td>
                            <td className="px-3 py-2 text-xs text-slate-900 dark:text-slate-100 break-words">{finding.summary}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="text-sm text-slate-600 dark:text-slate-300">No parsed findings yet. Run the binary with sanitizer instrumentation to populate this view.</p>
                )}
              </div>
            </div>
          )}

          {/* Terminal */}
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden flex flex-col">
            <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700 bg-slate-900">
              <h2 className="text-lg font-semibold text-slate-100">Terminal (UART0)</h2>
            </div>
            <div className="flex-1 bg-slate-950 text-slate-100 p-4 overflow-auto max-h-96 min-h-96 font-mono text-sm">
              {terminalOutput.length === 0 ? (
                <div className="text-slate-400">Waiting for output...</div>
              ) : (
                terminalOutput.map((line, idx) => (
                  <div key={idx} className="whitespace-pre-wrap break-words">
                    {line}
                  </div>
                ))
              )}
            </div>
            <div className="px-4 py-3 border-t border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800">
              <input
                type="text"
                placeholder="Enter command..."
                className="w-full px-3 py-2 border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded-lg"
              />
            </div>
          </div>

          {debuggerExpanded && (
            <>
              {/* Debug stream */}
              <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden flex flex-col">
                <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700 bg-slate-800">
                  <h2 className="text-lg font-semibold text-slate-100">Debugger Stream</h2>
                </div>
                <div className="flex-1 bg-slate-900 text-slate-100 p-4 overflow-auto max-h-72 min-h-48 font-mono text-xs">
                  {debugStream.length === 0 ? (
                    <div className="text-slate-400">No debugger traffic yet.</div>
                  ) : (
                    debugStream.map((line, idx) => (
                      <div key={idx} className="whitespace-pre-wrap break-words">
                        {line}
                      </div>
                    ))
                  )}
                </div>
                <div className="px-4 py-2 border-t border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 flex justify-end">
                  <button
                    onClick={() => setDebugStream([])}
                    className="text-xs text-slate-700 dark:text-slate-200 hover:text-slate-900 dark:hover:text-white"
                  >
                    Clear Debug Stream
                  </button>
                </div>
              </div>

              <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden">
                <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800">
                  <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Breakpoints</h2>
                </div>
                <div className="p-4 space-y-3">
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={breakpointInput}
                      onChange={(e) => setBreakpointInput(e.target.value)}
                      placeholder="Breakpoint location (e.g., main or src/main.c:42)"
                      className="flex-1 px-3 py-2 border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 text-slate-900 dark:text-slate-100 rounded-lg text-sm"
                    />
                    <button
                      onClick={addBreakpoint}
                      disabled={debugActionLoading === 'add-breakpoint'}
                      className="bg-indigo-700 dark:bg-indigo-600 text-white px-3 py-2 rounded-lg hover:bg-indigo-800 dark:hover:bg-indigo-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                    >
                      Add
                    </button>
                    <button
                      onClick={refreshBreakpoints}
                      disabled={debugActionLoading === 'list-breakpoints'}
                      className="bg-slate-700 dark:bg-slate-600 text-white px-3 py-2 rounded-lg hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed text-sm"
                    >
                      Refresh
                    </button>
                  </div>

                  {breakpoints.length === 0 ? (
                    <p className="text-sm text-slate-600 dark:text-slate-300">No breakpoints configured.</p>
                  ) : (
                    <div className="space-y-2">
                      {breakpoints.map((bp) => (
                        <div key={bp.number} className="border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 flex items-center justify-between gap-3">
                          <div>
                            <p className="text-sm font-medium text-slate-900 dark:text-slate-100">#{bp.number}</p>
                            <p className="text-xs text-slate-700 dark:text-slate-300 break-all">{bp.location}</p>
                          </div>
                          <button
                            onClick={() => removeBreakpoint(bp.number)}
                            disabled={debugActionLoading === `delete-breakpoint-${bp.number}`}
                            className="text-xs bg-rose-700 dark:bg-rose-600 text-white px-2 py-1 rounded hover:bg-rose-800 dark:hover:bg-rose-500 disabled:bg-slate-400 disabled:cursor-not-allowed"
                          >
                            Remove
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>

              <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden">
                <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 flex items-center justify-between">
                  <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Stack Trace</h2>
                  <button
                    onClick={refreshStackTrace}
                    disabled={debugActionLoading === 'stack-trace'}
                    className="text-xs bg-slate-700 dark:bg-slate-600 text-white px-3 py-1 rounded hover:bg-slate-800 dark:hover:bg-slate-500 disabled:bg-slate-400 disabled:cursor-not-allowed"
                  >
                    Refresh Stack
                  </button>
                </div>
                <div className="p-4">
                  {stackFrames.length === 0 ? (
                    <p className="text-sm text-slate-600 dark:text-slate-300">No stack frames available yet.</p>
                  ) : (
                    <div className="space-y-2">
                      {stackFrames.map((frame) => (
                        <div key={`${frame.index}-${frame.function}`} className="border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2">
                          <p className="text-sm font-medium text-slate-900 dark:text-slate-100">#{frame.index} {frame.function}</p>
                          <p className="text-xs text-slate-700 dark:text-slate-300 break-all">{frame.location || 'location unavailable'}</p>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
