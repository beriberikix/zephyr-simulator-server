import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import UploadPage from './pages/Upload'
import EmulatorPage from './pages/Emulator'
import SessionsPage from './pages/Sessions'
import DashboardPage from './pages/Dashboard'
import LoginPage from './pages/Login'

function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/upload" element={<UploadPage />} />
        <Route path="/emulator/:sessionId" element={<EmulatorPage />} />
        <Route path="/sessions" element={<SessionsPage />} />
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

export default App
