import { createContext, useContext, useState, useEffect, ReactNode } from 'react'
import {
  PBUser,
  pb,
  getCurrentUser,
  login as pbLogin,
  register as pbRegister,
  refreshSession,
  logout as pbLogout,
} from '../lib/auth'

interface AuthState {
  user: PBUser | null
  isAuthenticated: boolean
  isAdmin: boolean
  loading: boolean
  login: (email: string, password: string) => Promise<void>
  register: (email: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<PBUser | null>(getCurrentUser())
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const unsubscribe = pb.authStore.onChange(() => {
      setUser(getCurrentUser())
    }, true)

    if (!pb.authStore.token) {
      setLoading(false)
      return () => {
        unsubscribe()
      }
    }

    void refreshSession().then((result) => {
      if (result) {
        setUser(result.user)
      }
      setLoading(false)
    })

    return () => {
      unsubscribe()
    }
  }, [])

  const login = async (email: string, password: string) => {
    const result = await pbLogin(email, password)
    setUser(result.user)
  }

  const register = async (email: string, password: string) => {
    await pbRegister(email, password)
    const result = await pbLogin(email, password)
    setUser(result.user)
  }

  const logout = () => {
    pbLogout()
    setUser(null)
  }

  return (
    <AuthContext.Provider
      value={{
        user,
        isAuthenticated: user !== null,
        isAdmin: user?.role === 'admin',
        loading,
        login,
        register,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
