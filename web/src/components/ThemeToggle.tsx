import { useTheme } from '../context/ThemeContext'

export default function ThemeToggle() {
  const { preference, setPreference } = useTheme()

  return (
    <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
      <span className="font-medium">Theme</span>
      <select
        aria-label="Color theme"
        value={preference}
        onChange={(e) => setPreference(e.target.value as 'system' | 'light' | 'dark')}
        className="rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 px-2 py-1 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-sky-500"
      >
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
    </label>
  )
}
