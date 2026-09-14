export type EventType =
  | 'log'
  | 'account'
  | 'status'
  | 'progress'
  | 'complete'

export interface BotEvent {
  type: EventType
  job_id: string
  timestamp: string

  // log
  worker_id?: number
  tag?: string
  step?: string
  status_code?: number
  message?: string

  // account
  email?: string
  password?: string
  success?: boolean
  error?: string

  // status / progress
  status?: JobStatus
  target?: number
  attempts?: number
  success_count?: number
  failure_count?: number
  elapsed_ms?: number
}

export type JobStatus =
  | 'running'
  | 'paused'
  | 'stopping'
  | 'completed'
  | 'stopped'
  | 'failed'

export interface Job {
  id: string
  started_at: string
  finished_at?: string
  target: number
  workers: number
  proxy: string
  domain: string
  status: JobStatus
  success_count: number
  failure_count: number
  attempts: number
  elapsed_ms: number
}

export interface Account {
  id: number
  job_id: string
  email: string
  password: string
  success: boolean
  error?: string
  created_at: string
}

export interface Snapshot {
  active: boolean
  job_id?: string
  status?: JobStatus
  target?: number
  attempts?: number
  success?: number
  failures?: number
  elapsed_ms?: number
}

export interface StartJobRequest {
  target: number
  workers: number
  proxy: string
  domain: string
  password: string
}

/** A single worker lane rendered in the progress grid. */
export interface WorkerLane {
  workerId: number
  tag: string
  step: string
  statusCode: number
  updatedAt: number
  phase: WorkerPhase
}

export type WorkerPhase =
  | 'idle'
  | 'warmup'
  | 'auth'
  | 'register'
  | 'otp'
  | 'account'
  | 'callback'
  | 'done'
  | 'error'
