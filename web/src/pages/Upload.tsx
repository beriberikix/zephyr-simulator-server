import { useState } from 'react'
import axios from 'axios'
import { Link } from 'react-router-dom'

interface BinaryRecord {
  id: string
}

interface SessionRecord {
  id: string
  state: string
}

export default function UploadPage() {
  const [file, setFile] = useState<File | null>(null)
  const [uploading, setUploading] = useState(false)
  const [message, setMessage] = useState('')
  const [lastSessionId, setLastSessionId] = useState('')
  const [createSession, setCreateSession] = useState(true)
  const [autoStartSession, setAutoStartSession] = useState(false)

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      setFile(files[0])
    }
  }

  const handleUpload = async () => {
    if (!file) {
      setMessage('Please select a file first')
      return
    }

    setUploading(true)
    const formData = new FormData()
    formData.append('binary', file)

    try {
      const response = await axios.post('/api/binaries', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })

      const binary = response.data.data as BinaryRecord
      let statusMessage = `Uploaded successfully! Binary ID: ${binary?.id}`

      if (createSession && binary?.id) {
        const sessionResponse = await axios.post('/api/sessions', {
          binary_id: binary.id,
          seed: Date.now(),
          use_real_time: false,
        })
        const session = sessionResponse.data.data as SessionRecord
        setLastSessionId(session.id)
        statusMessage += ` | Session created: ${session.id}`

        if (autoStartSession) {
          const startResponse = await axios.post(`/api/sessions/${session.id}/start`)
          const startedSession = startResponse.data.data as SessionRecord | undefined
          if (startResponse.data.success && startedSession?.state === 'running') {
            statusMessage += ' | Session started'
          } else if (!startResponse.data.success) {
            statusMessage += ` | Start failed: ${startResponse.data.error}`
          }
        }
      }

      setMessage(statusMessage)
      setFile(null)
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const apiError = err.response?.data?.error
        setMessage(`Upload failed: ${apiError || err.message}`)
      } else {
        setMessage(`Upload failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
      }
    } finally {
      setUploading(false)
    }
  }

  return (
    <div>
      <h1 className="text-3xl font-bold text-slate-900 dark:text-slate-100 mb-8">Upload Binary</h1>

      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow p-8 max-w-md">
        <div className="space-y-6">
          <div>
            <label className="block text-sm font-medium text-slate-700 dark:text-slate-200 mb-2">
              Select Zephyr Binary
            </label>
            <input
              type="file"
              onChange={handleFileChange}
              className="block w-full text-sm text-slate-600 dark:text-slate-300
                file:mr-4 file:py-2 file:px-4
                file:rounded-lg file:border-0
                file:text-sm file:font-semibold
                file:bg-sky-100 dark:file:bg-sky-900/40 file:text-sky-800 dark:file:text-sky-200
                hover:file:bg-sky-200 dark:hover:file:bg-sky-800/60"
            />
            {file && (
              <p className="mt-2 text-sm text-slate-700 dark:text-slate-300">
                Selected: {file.name} ({(file.size / 1024).toFixed(2)} KB)
              </p>
            )}
          </div>

          <div className="space-y-3 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/60 p-3">
            <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
              <input
                type="checkbox"
                checked={createSession}
                onChange={(e) => setCreateSession(e.target.checked)}
              />
              Create session after upload
            </label>

            <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
              <input
                type="checkbox"
                checked={autoStartSession}
                onChange={(e) => setAutoStartSession(e.target.checked)}
                disabled={!createSession}
              />
              Auto-start session after creation
            </label>
          </div>

          <button
            onClick={handleUpload}
            disabled={!file || uploading}
            className="w-full bg-sky-700 text-white py-2 px-4 rounded-lg hover:bg-sky-800 dark:bg-sky-600 dark:hover:bg-sky-500 disabled:bg-slate-400"
          >
            {uploading ? 'Uploading...' : 'Upload'}
          </button>

          {message && (
            <div className="p-3 bg-sky-50 dark:bg-sky-900/40 border border-sky-200 dark:border-sky-700 text-sky-900 dark:text-sky-200 rounded-lg text-sm">
              {message}
            </div>
          )}

          {lastSessionId && (
            <Link
              to="/sessions"
              className="block w-full text-center bg-slate-200 dark:bg-slate-700 text-slate-900 dark:text-slate-100 py-2 px-4 rounded-lg hover:bg-slate-300 dark:hover:bg-slate-600 text-sm"
            >
              View sessions
            </Link>
          )}
        </div>
      </div>
    </div>
  )
}
