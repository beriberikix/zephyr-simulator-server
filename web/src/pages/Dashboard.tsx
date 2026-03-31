import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import axios from 'axios'
import { SessionSummary, SessionState } from '../types/session'

export default function DashboardPage() {
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const fetchSessions = async () => {
      try {
        const response = await axios.get('/api/sessions')
        setSessions(response.data.data || [])
      } catch (err) {
        console.error('Failed to fetch sessions:', err)
      } finally {
        setLoading(false)
      }
    }

    fetchSessions()
  }, [])

  const getStatusColor = (state: SessionState) => {
    switch (state) {
      case 'running':
        return 'bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200'
      case 'paused':
        return 'bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200'
      case 'stopped':
        return 'bg-rose-100 text-rose-900 dark:bg-rose-900/40 dark:text-rose-200'
      default:
        return 'bg-slate-100 text-slate-800 dark:bg-slate-700 dark:text-slate-100'
    }
  }

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100 mb-2">Dashboard</h1>
        <p className="text-slate-700 dark:text-slate-300">Manage and monitor your Zephyr emulator sessions</p>
      </div>

      <div className="grid gap-6 md:grid-cols-3 mb-8">
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 p-6 rounded-lg shadow">
          <h3 className="text-sm font-medium text-slate-600 dark:text-slate-300 mb-2">Active Sessions</h3>
          <p className="text-3xl font-bold text-sky-700 dark:text-sky-300">
            {sessions.filter(s => s.state === 'running').length}
          </p>
        </div>
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 p-6 rounded-lg shadow">
          <h3 className="text-sm font-medium text-slate-600 dark:text-slate-300 mb-2">Paused Sessions</h3>
          <p className="text-3xl font-bold text-amber-700 dark:text-amber-300">
            {sessions.filter(s => s.state === 'paused').length}
          </p>
        </div>
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 p-6 rounded-lg shadow">
          <h3 className="text-sm font-medium text-slate-600 dark:text-slate-300 mb-2">Total Sessions</h3>
          <p className="text-3xl font-bold text-slate-700 dark:text-slate-200">
            {sessions.length}
          </p>
        </div>
      </div>

      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow overflow-hidden">
        <div className="px-6 py-4 border-b border-slate-200 dark:border-slate-700">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Recent Sessions</h2>
        </div>
        {loading ? (
          <div className="p-6 text-center text-slate-600 dark:text-slate-300">Loading...</div>
        ) : sessions.length === 0 ? (
          <div className="p-6">
            <p className="text-center text-slate-700 dark:text-slate-300 mb-4">No sessions yet</p>
            <div className="flex gap-4 justify-center">
              <Link
                to="/upload"
                className="bg-sky-700 text-white px-4 py-2 rounded-lg hover:bg-sky-800 dark:bg-sky-600 dark:hover:bg-sky-500"
              >
                Upload Binary
              </Link>
              <Link
                to="/sessions"
                className="bg-slate-200 text-slate-900 px-4 py-2 rounded-lg hover:bg-slate-300 dark:bg-slate-700 dark:text-slate-100 dark:hover:bg-slate-600"
              >
                View Sessions
              </Link>
            </div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full">
              <thead className="bg-slate-100 dark:bg-slate-800 border-b border-slate-200 dark:border-slate-700">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">
                    ID
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">
                    Status
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">
                    Seed
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">
                    Created
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-900 dark:text-slate-100 uppercase">
                    Action
                  </th>
                </tr>
              </thead>
              <tbody>
                {sessions.slice(0, 5).map((session) => (
                  <tr key={session.id} className="border-b border-slate-200 dark:border-slate-700 hover:bg-slate-50 dark:hover:bg-slate-800/60">
                    <td className="px-6 py-4 text-sm font-mono text-slate-900 dark:text-slate-100">
                      {session.id.substring(0, 8)}...
                    </td>
                    <td className="px-6 py-4 text-sm">
                      <span className={`inline-block px-3 py-1 rounded-full text-xs font-medium ${getStatusColor(session.state)}`}>
                        {session.state}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-sm text-slate-700 dark:text-slate-300">
                      {session.seed}
                    </td>
                    <td className="px-6 py-4 text-sm text-slate-700 dark:text-slate-300">
                      {new Date(session.created_at).toLocaleDateString()}
                    </td>
                    <td className="px-6 py-4 text-sm">
                      <Link
                        to={`/emulator/${session.id}`}
                        className="text-sky-700 dark:text-sky-300 hover:text-sky-900 dark:hover:text-sky-200 font-medium"
                      >
                        Open
                      </Link>
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
