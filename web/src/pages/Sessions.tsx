import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { SessionSummary } from '../types/session'

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [actionMessage, setActionMessage] = useState('')
  const [actionLoadingId, setActionLoadingId] = useState<string | null>(null)

  const loadSessions = useCallback(async () => {
    setLoading(true)
    try {
      const response = await axios.get('/api/sessions')
      setSessions(response.data.data || [])
    } catch (err) {
      if (axios.isAxiosError(err)) {
        setActionMessage(`Failed to load sessions: ${err.response?.data?.error || err.message}`)
      } else {
        setActionMessage('Failed to load sessions')
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadSessions()
  }, [loadSessions])

  const runAction = async (session: SessionSummary, action: 'start' | 'stop' | 'pause' | 'resume' | 'delete') => {
    setActionLoadingId(`${session.id}:${action}`)
    try {
      if (action === 'delete') {
        await axios.delete(`/api/sessions/${session.id}`)
        setActionMessage(`Deleted session ${session.id}`)
      } else {
        const response = await axios.post(`/api/sessions/${session.id}/${action}`)
        if (!response.data.success) {
          setActionMessage(`${action} failed: ${response.data.error || 'unknown error'}`)
        } else {
          setActionMessage(`${action} succeeded for ${session.id}`)
        }
      }
    } catch (err) {
      if (axios.isAxiosError(err)) {
        setActionMessage(`${action} failed: ${err.response?.data?.error || err.message}`)
      } else {
        setActionMessage(`${action} failed`)
      }
    } finally {
      setActionLoadingId(null)
      await loadSessions()
    }
  }

  const isActionAllowed = (state: Session['state'], action: 'start' | 'stop' | 'pause' | 'resume' | 'delete') => {
    if (action === 'delete') return true
    if (action === 'start') return state === 'stopped'
    if (action === 'pause') return state === 'running'
    if (action === 'resume') return state === 'paused'
    if (action === 'stop') return state === 'running' || state === 'paused'
    return false
  }

  const buttonClass = (base: string, enabled: boolean) => {
    if (!enabled) return `${base} opacity-50 cursor-not-allowed`
    return base
  }

  const statusClass = (state: Session['state']) => {
    if (state === 'running') return 'bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200'
    if (state === 'paused') return 'bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200'
    return 'bg-slate-100 text-slate-800 dark:bg-slate-700 dark:text-slate-100'
  }

  return (
    <div>
      <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100 mb-2">Sessions</h1>
      <p className="text-slate-700 dark:text-slate-300 mb-6">View and manage all emulator sessions</p>

      {actionMessage && (
        <div className="mb-4 rounded-lg bg-sky-50 dark:bg-sky-900/40 border border-sky-200 dark:border-sky-700 px-4 py-3 text-sm text-sky-900 dark:text-sky-200">
          {actionMessage}
        </div>
      )}

      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-700 dark:text-slate-300">Loading sessions...</div>
        ) : sessions.length === 0 ? (
          <div className="p-8 text-center text-slate-700 dark:text-slate-300">No sessions yet. Upload a binary to create one.</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full">
              <thead className="bg-slate-100 dark:bg-slate-800 border-b border-slate-200 dark:border-slate-700">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">Session</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">Binary</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">State</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">Seed</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">Actions</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((session) => (
                  <tr key={session.id} className="border-b border-slate-200 dark:border-slate-700 align-top">
                    <td className="px-6 py-4 text-sm font-mono text-slate-900 dark:text-slate-100">{session.id}</td>
                    <td className="px-6 py-4 text-sm text-slate-700 dark:text-slate-300">{session.binary_id}</td>
                    <td className="px-6 py-4 text-sm">
                      <span className={`inline-block rounded-full px-3 py-1 text-xs font-medium ${statusClass(session.state)}`}>
                        {session.state}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-sm text-slate-700 dark:text-slate-300">{session.seed}</td>
                    <td className="px-6 py-4 text-sm">
                      <div className="flex flex-wrap gap-2">
                        {(() => {
                          const startEnabled = isActionAllowed(session.state, 'start') && actionLoadingId === null
                          const pauseEnabled = isActionAllowed(session.state, 'pause') && actionLoadingId === null
                          const resumeEnabled = isActionAllowed(session.state, 'resume') && actionLoadingId === null
                          const stopEnabled = isActionAllowed(session.state, 'stop') && actionLoadingId === null
                          const deleteEnabled = isActionAllowed(session.state, 'delete') && actionLoadingId === null
                          return (
                            <>
                        <button
                          onClick={() => runAction(session, 'start')}
                          disabled={!startEnabled}
                          className={buttonClass('rounded bg-emerald-700 dark:bg-emerald-600 px-3 py-1 text-white hover:bg-emerald-800 dark:hover:bg-emerald-500', startEnabled)}
                        >
                          Start
                        </button>
                        <button
                          onClick={() => runAction(session, 'pause')}
                          disabled={!pauseEnabled}
                          className={buttonClass('rounded bg-amber-700 dark:bg-amber-600 px-3 py-1 text-white hover:bg-amber-800 dark:hover:bg-amber-500', pauseEnabled)}
                        >
                          Pause
                        </button>
                        <button
                          onClick={() => runAction(session, 'resume')}
                          disabled={!resumeEnabled}
                          className={buttonClass('rounded bg-sky-700 dark:bg-sky-600 px-3 py-1 text-white hover:bg-sky-800 dark:hover:bg-sky-500', resumeEnabled)}
                        >
                          Resume
                        </button>
                        <button
                          onClick={() => runAction(session, 'stop')}
                          disabled={!stopEnabled}
                          className={buttonClass('rounded bg-rose-700 dark:bg-rose-600 px-3 py-1 text-white hover:bg-rose-800 dark:hover:bg-rose-500', stopEnabled)}
                        >
                          Stop
                        </button>
                        <button
                          onClick={() => runAction(session, 'delete')}
                          disabled={!deleteEnabled}
                          className={buttonClass('rounded bg-slate-700 dark:bg-slate-600 px-3 py-1 text-white hover:bg-slate-800 dark:hover:bg-slate-500', deleteEnabled)}
                        >
                          Delete
                        </button>
                            </>
                          )
                        })()}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
