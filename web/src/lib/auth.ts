import axios from 'axios'
import PocketBase, { ClientResponseError, type RecordModel } from 'pocketbase'

// ─── Anonymous ID ─────────────────────────────────────────────────────────────

const ANON_ID_KEY = 'zephyr_anon_id'

export const pb = new PocketBase('/')
pb.autoCancellation(false)

export function getAnonId(): string {
  let id = localStorage.getItem(ANON_ID_KEY)
  if (!id) {
    id = crypto.randomUUID()
    localStorage.setItem(ANON_ID_KEY, id)
  }
  return id
}

// ─── PocketBase user record ───────────────────────────────────────────────────

export interface PBUser {
  id: string
  email: string
  role: string  // "user" | "admin"
}

function toPBUser(record: RecordModel | null | undefined): PBUser | null {
  if (!record) {
    return null
  }

  const userRecord = record as RecordModel & { email?: string; role?: string }
  return {
    id: userRecord.id,
    email: userRecord.email ?? '',
    role: userRecord.role ?? 'user',
  }
}

export function getCurrentUser(): PBUser | null {
  return toPBUser(pb.authStore.record)
}

// ─── Global axios interceptors ────────────────────────────────────────────────
// Called once at startup so all API calls carry either auth token or anon id.

export function setupAxiosInterceptors(): void {
  axios.interceptors.request.use((config) => {
    const token = pb.authStore.token
    if (pb.authStore.isValid && token) {
      config.headers['Authorization'] = `Bearer ${token}`
    } else {
      config.headers['X-Anonymous-ID'] = getAnonId()
    }
    return config
  })
}

// ─── Auth API ─────────────────────────────────────────────────────────────────

export async function login(
  email: string,
  password: string,
): Promise<{ user: PBUser }> {
  const authData = await pb.collection('users').authWithPassword(email, password)
  const user = toPBUser(authData.record)

  if (!user) {
    throw new Error('Authenticated user record is missing.')
  }

  return {
    user,
  }
}

export async function register(email: string, password: string): Promise<void> {
  await pb.collection('users').create({
    email,
    password,
    passwordConfirm: password,
  })
}

export async function refreshSession(
): Promise<{ user: PBUser } | null> {
  if (!pb.authStore.token) {
    return null
  }

  try {
    const authData = await pb.collection('users').authRefresh()
    const user = toPBUser(authData.record)
    if (!user) {
      return null
    }

    return {
      user,
    }
  } catch {
    pb.authStore.clear()
    return null
  }
}

export function logout(): void {
  pb.authStore.clear()
}

export function extractAuthErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ClientResponseError) {
    const fieldErrors = err.response?.data as Record<string, { message?: string }> | undefined
    if (fieldErrors) {
      const firstFieldError = Object.values(fieldErrors)[0]
      if (firstFieldError?.message) {
        return firstFieldError.message
      }
    }

    if (typeof err.response?.message === 'string') {
      return err.response.message
    }

    if (typeof err.message === 'string' && err.message.length > 0) {
      return err.message
    }
  }

  if (err instanceof Error && err.message) {
    return err.message
  }

  return fallback
}
