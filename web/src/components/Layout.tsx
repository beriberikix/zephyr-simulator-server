import { Outlet, Link } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import ThemeToggle from './ThemeToggle'

export default function Layout() {
  const { isAuthenticated, user, logout } = useAuth()

  return (
    <div className="min-h-screen bg-slate-50 text-slate-900 dark:bg-slate-950 dark:text-slate-100 flex flex-col">
      {/* Header */}
      <header className="bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-700 shadow-sm">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4">
          <div className="flex items-center justify-between">
            <Link to="/" className="flex items-center gap-2">
              <div className="w-8 h-8 bg-sky-700 dark:bg-sky-500 rounded-lg flex items-center justify-center">
                <span className="text-white font-bold text-sm">ZE</span>
              </div>
              <h1 className="text-xl font-bold text-slate-900 dark:text-slate-100">
                Zephyr Remote Emulator
              </h1>
            </Link>
            <nav className="flex gap-6 items-center">
              <ThemeToggle />
              <Link
                to="/"
                className="text-sm font-medium text-slate-700 dark:text-slate-200 hover:text-slate-900 dark:hover:text-white focus:outline-none focus:ring-2 focus:ring-sky-500 rounded"
              >
                Dashboard
              </Link>
              <Link
                to="/upload"
                className="text-sm font-medium text-slate-700 dark:text-slate-200 hover:text-slate-900 dark:hover:text-white focus:outline-none focus:ring-2 focus:ring-sky-500 rounded"
              >
                Upload Binary
              </Link>
              <Link
                to="/sessions"
                className="text-sm font-medium text-slate-700 dark:text-slate-200 hover:text-slate-900 dark:hover:text-white focus:outline-none focus:ring-2 focus:ring-sky-500 rounded"
              >
                Sessions
              </Link>
              {isAuthenticated ? (
                <div className="flex items-center gap-3">
                  <span className="text-sm text-slate-600 dark:text-slate-300">{user?.email}</span>
                  <button
                    type="button"
                    onClick={logout}
                    className="text-sm font-medium text-red-700 dark:text-red-300 hover:text-red-900 dark:hover:text-red-200 focus:outline-none focus:ring-2 focus:ring-red-500 rounded"
                  >
                    Sign Out
                  </button>
                </div>
              ) : (
                <Link
                  to="/login"
                  className="text-sm font-medium text-sky-700 dark:text-sky-300 hover:text-sky-900 dark:hover:text-sky-200 focus:outline-none focus:ring-2 focus:ring-sky-500 rounded"
                >
                  Sign In
                </Link>
              )}
            </nav>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="flex-1">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
          <Outlet />
        </div>
      </main>

      {/* Footer */}
      <footer className="bg-white dark:bg-slate-900 border-t border-slate-200 dark:border-slate-700 py-4 mt-8">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 text-center text-sm text-slate-600 dark:text-slate-300">
          <p>Zephyr Remote Emulator • Powered by Go + React</p>
        </div>
      </footer>
    </div>
  )
}
