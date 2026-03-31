export type SessionState = 'running' | 'paused' | 'stopped'

export interface SessionSummary {
  id: string
  binary_id: string
  state: SessionState
  seed: number
  created_at: string
  updated_at: string
}

export interface SessionDetail extends SessionSummary {
  pcap_enabled?: boolean
  pcap_file_path?: string
  coverage_enabled?: boolean
  asan_enabled?: boolean
  ubsan_enabled?: boolean
}
