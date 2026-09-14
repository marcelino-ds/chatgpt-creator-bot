import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from './api'
import { PHASE_LABEL, PHASE_ORDER, fmtClock, fmtDuration, phaseOf } from './phases'
import { TerminalPane, type TermHandle } from './TerminalPane'
import type {
  Account,
  BotEvent,
  Job,
  JobStatus,
  WorkerLane,
  WorkerPhase,
} from './types'
import { useEventSocket } from './useEventSocket'

type Tab = 'workers' | 'accounts' | 'history'

interface Live {
  active: boolean
  jobId: string
  status: JobStatus | 'idle'
  target: number
  attempts: number
  success: number
  failures: number
  elapsedMs: number
}

const IDLE: Live = {
  active: false,
  jobId: '',
  status: 'idle',
  target: 0,
  attempts: 0,
  success: 0,
  failures: 0,
  elapsedMs: 0,
}

export default function App() {
  // UI build 2: custom stop modal, no browser confirm()
  const term = useRef<TermHandle | null>(null)
  const [live, setLive] = useState<Live>(IDLE)
  const [lanes, setLanes] = useState<Record<number, WorkerLane>>({})
  const [accounts, setAccounts] = useState<Account[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [tab, setTab] = useState<Tab>('workers')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [tick, setTick] = useState(0)
  const [confirmStop, setConfirmStop] = useState(false)

  // form
  const [target, setTarget] = useState('5')
  const [workers, setWorkers] = useState('3')
  const [proxy, setProxy] = useState('')
  const [domain, setDomain] = useState('')
  const [password, setPassword] = useState('')

  const running = live.status === 'running'
  const paused = live.status === 'paused'
  const stopping = live.status === 'stopping'
  const activeJob = running || paused || stopping

  const flash = useCallback((m: string) => {
    setErr(m)
    window.setTimeout(() => setErr(''), 5000)
  }, [])

  const refreshTables = useCallback(async () => {
    try {
      const [js, accs] = await Promise.all([api.jobs(50), api.accounts(200)])
      setJobs(js ?? [])
      setAccounts(accs ?? [])
    } catch (e) {
      flash((e as Error).message)
    }
  }, [flash])

  const onEvent = useCallback((e: BotEvent) => {
    switch (e.type) {
      case 'log': {
        const step = e.step ?? ''
        const code = e.status_code ?? 0
        const wid = e.worker_id ?? 0
        term.current?.writeLog({
          time: fmtClock(e.timestamp),
          worker: wid,
          tag: e.tag ?? '-',
          step,
          statusCode: code,
        })
        if (wid > 0) {
          setLanes((prev) => ({
            ...prev,
            [wid]: {
              workerId: wid,
              tag: e.tag ?? '',
              step,
              statusCode: code,
              updatedAt: Date.now(),
              phase: phaseOf(step, code),
            },
          }))
        }
        break
      }
      case 'account': {
        term.current?.writeAccount(e.email ?? '', !!e.success, e.error)
        const row: Account = {
          id: Date.now() + Math.random(),
          job_id: e.job_id,
          email: e.email ?? '',
          password: e.password ?? '',
          success: !!e.success,
          error: e.error,
          created_at: e.timestamp,
        }
        setAccounts((prev) => [row, ...prev].slice(0, 300))
        break
      }
      case 'progress':
      case 'status': {
        setLive((prev) => ({
          active: true,
          jobId: e.job_id || prev.jobId,
          status: (e.status as JobStatus) ?? prev.status,
          target: e.target ?? prev.target,
          attempts: e.attempts ?? prev.attempts,
          success: e.success_count ?? prev.success,
          failures: e.failure_count ?? prev.failures,
          elapsedMs: e.elapsed_ms ?? prev.elapsedMs,
        }))
        break
      }
      case 'complete': {
        setLive((prev) => ({
          ...prev,
          active: false,
          status: (e.status as JobStatus) ?? 'completed',
          attempts: e.attempts ?? prev.attempts,
          success: e.success_count ?? prev.success,
          failures: e.failure_count ?? prev.failures,
          elapsedMs: e.elapsed_ms ?? prev.elapsedMs,
        }))
        setLanes({})
        term.current?.writeLine('')
        term.current?.writeBanner([
          `── job ${e.status ?? 'finished'} · ${e.success_count ?? 0}/${e.target ?? 0} ok · ${
            e.failure_count ?? 0
          } failed · ${fmtDuration(e.elapsed_ms ?? 0)} ──`,
        ])
        void refreshTables()
        break
      }
    }
  }, [refreshTables])

  const conn = useEventSocket(onEvent)

  // Boot: banner + restore state.
  useEffect(() => {
    term.current?.writeBanner([
      '  ┌─────────────────────────────────────────────┐',
      '  │  creator control center · execution stream  │',
      '  └─────────────────────────────────────────────┘',
    ])
    term.current?.writeLine('')

    void (async () => {
      try {
        const s = await api.snapshot()
        if (s.active) {
          setLive({
            active: true,
            jobId: s.job_id ?? '',
            status: s.status ?? 'running',
            target: s.target ?? 0,
            attempts: s.attempts ?? 0,
            success: s.success ?? 0,
            failures: s.failures ?? 0,
            elapsedMs: s.elapsed_ms ?? 0,
          })
        }
      } catch {
        /* server may still be starting */
      }
      void refreshTables()
    })()
  }, [refreshTables])

  // Live elapsed ticker while a job runs.
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(() => setTick((t) => t + 1), 1000)
    return () => window.clearInterval(id)
  }, [running])

  const elapsedShown = useMemo(() => {
    void tick
    return fmtDuration(live.elapsedMs)
  }, [live.elapsedMs, tick])

  const pct = live.target > 0 ? Math.min(1, live.success / live.target) : 0
  const failPct = live.target > 0 ? Math.min(1, live.failures / live.target) : 0

  async function start() {
    const t = parseInt(target, 10)
    const w = parseInt(workers, 10)
    if (!Number.isFinite(t) || t < 1) return flash('Target must be at least 1')
    if (!Number.isFinite(w) || w < 1) return flash('Workers must be at least 1')
    if (password && password.length < 12) {
      return flash('Password must be 12+ characters, or leave it empty')
    }
    setBusy(true)
    try {
      term.current?.clear()
      term.current?.writeBanner([`── starting job · target ${t} · ${w} workers ──`])
      term.current?.writeLine('')
      setLanes({})
      setLive({ ...IDLE, active: true, status: 'running', target: t })
      await api.startJob({
        target: t,
        workers: w,
        proxy: proxy.trim(),
        domain: domain.trim(),
        password: password.trim(),
      })
    } catch (e) {
      setLive(IDLE)
      flash((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function ctrl(kind: 'pause' | 'resume' | 'stop') {
    setBusy(true)
    try {
      if (kind === 'pause') await api.pause()
      if (kind === 'resume') await api.resume()
      if (kind === 'stop') await api.stop()
      setLive((p) => ({
        ...p,
        status:
          kind === 'pause' ? 'paused' : kind === 'resume' ? 'running' : kind === 'stop' ? 'stopping' : p.status,
      }))
    } catch (e) {
      flash((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const laneList = Object.values(lanes).sort((a, b) => a.workerId - b.workerId)

  return (
    <div className="shell">
      <div className="brand">
        <div className="brand-mark">◆</div>
        <div className="brand-text">
          <b>Creator</b>
          <span>control center</span>
        </div>
      </div>

      <div className="topbar">
        <div className="topbar-metrics">
          <Stat k="success" v={live.success} tone="green" />
          <Stat k="failed" v={live.failures} tone="red" />
          <Stat k="attempts" v={live.attempts} tone="violet" />
          <Stat k="target" v={live.target} />
          <Stat k="elapsed" v={elapsedShown} tone="amber" />
        </div>
        <ConnPill conn={conn} status={live.status} />
      </div>

      <aside className="sidebar">
        <div>
          <div className="section-label">new job</div>
          <div className="row-2">
            <Field label="Target" hint="accounts">
              <input
                className="input"
                type="number"
                min={1}
                value={target}
                disabled={activeJob}
                onChange={(e) => setTarget(e.target.value)}
              />
            </Field>
            <Field label="Workers" hint="parallel">
              <input
                className="input"
                type="number"
                min={1}
                value={workers}
                disabled={activeJob}
                onChange={(e) => setWorkers(e.target.value)}
              />
            </Field>
          </div>
          <Field label="Proxy" hint="optional">
            <input
              className="input"
              placeholder="http://user:pass@host:port"
              value={proxy}
              disabled={activeJob}
              onChange={(e) => setProxy(e.target.value)}
            />
          </Field>
          <Field label="Domain" hint="optional">
            <input
              className="input"
              placeholder="random temp-mail"
              value={domain}
              disabled={activeJob}
              onChange={(e) => setDomain(e.target.value)}
            />
          </Field>
          <Field label="Password" hint="12+ or random">
            <input
              className="input"
              placeholder="leave empty to randomize"
              value={password}
              disabled={activeJob}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          <button
            className="btn primary"
            disabled={activeJob || busy}
            onClick={() => void start()}
            style={{ marginTop: 4 }}
          >
            {activeJob ? 'Job in progress' : 'Start job'}
          </button>
        </div>

        <div>
          <div className="section-label">controls</div>
          <div className="btn-row">
            <button
              className="btn amber"
              disabled={!running || busy}
              onClick={() => void ctrl('pause')}
            >
              Pause
            </button>
            <button
              className="btn ghost"
              disabled={!paused || busy}
              onClick={() => void ctrl('resume')}
            >
              Resume
            </button>
          </div>
          <button
            className="btn danger"
            disabled={!activeJob || busy || stopping}
            onClick={() => setConfirmStop(true)}
            style={{ marginTop: 8 }}
          >
            {stopping ? 'Stopping…' : 'Stop'}
          </button>
        </div>

        <div>
          <div className="section-label">current job</div>
          <div className="kv">
            <span>id</span>
            <b>{live.jobId ? live.jobId.slice(0, 8) : '—'}</b>
          </div>
          <div className="kv">
            <span>status</span>
            <b>{live.status}</b>
          </div>
          <div className="kv">
            <span>progress</span>
            <b>
              {live.success}/{live.target || '—'}
            </b>
          </div>
          <div className="kv">
            <span>lanes</span>
            <b>{laneList.length}</b>
          </div>
        </div>

        <p className="hint">
          Success only counts when a session is validated after callback, so the
          number here is stricter than the old CLI output.
        </p>
      </aside>

      <main className="main">
        <section className="panel">
          <div className="panel-head">
            <div className="panel-title">
              <i className="dot" />
              live terminal
            </div>
            <button className="copy" onClick={() => term.current?.clear()}>
              clear
            </button>
          </div>

          <div className="progress-wrap">
            <div className="progress-meta">
              <span>
                <b>{Math.round(pct * 100)}%</b> complete
              </span>
              <span>
                {live.success} ok · {live.failures} failed · {live.attempts} attempts
              </span>
            </div>
            <div
              className={`track ${
                live.status === 'completed' ? 'done' : running ? '' : 'idle'
              }`}
            >
              <div className="fill" style={{ width: `${pct * 100}%` }} />
              {live.failures > 0 && (
                <div className="fail" style={{ width: `${Math.min(30, failPct * 100)}%` }} />
              )}
            </div>
          </div>

          <div className="panel-body flush">
            <TerminalPane ref={term} className="term-host" />
          </div>
        </section>

        <section className="panel">
          <div className="panel-head">
            <div className="panel-title violet">
              <i className="dot" />
              {tab === 'workers' ? 'worker lanes' : tab === 'accounts' ? 'accounts' : 'job history'}
            </div>
            <div className="tabs">
              {(['workers', 'accounts', 'history'] as Tab[]).map((t) => (
                <button
                  key={t}
                  className={`tab ${tab === t ? 'on' : ''}`}
                  onClick={() => setTab(t)}
                >
                  {t}
                </button>
              ))}
            </div>
          </div>

          <div className={`panel-body ${tab === 'workers' ? 'flush' : 'flush'}`}>
            {tab === 'workers' && <Lanes lanes={laneList} />}
            {tab === 'accounts' && <AccountsTable rows={accounts} />}
            {tab === 'history' && <HistoryTable rows={jobs} />}
          </div>
        </section>
      </main>

      {err && (
        <div className="toast" role="status">
          {err}
        </div>
      )}

      {confirmStop && (
        <div className="modal-backdrop" onClick={() => setConfirmStop(false)}>
          <div
            className="modal"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-modal="true"
            aria-labelledby="stop-title"
          >
            <div className="modal-kicker">confirm action</div>
            <h3 id="stop-title">Stop this job?</h3>
            <p>
              In-flight workers abort after the current request. Queued attempts
              are dropped. Accounts already written stay in history.
            </p>
            <div className="modal-actions">
              <button className="btn ghost" onClick={() => setConfirmStop(false)}>
                Keep running
              </button>
              <button
                className="btn danger"
                onClick={() => {
                  setConfirmStop(false)
                  void ctrl('stop')
                }}
              >
                Stop job
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/* ---------- small pieces ---------- */

function Stat({
  k,
  v,
  tone,
}: {
  k: string
  v: number | string
  tone?: 'green' | 'red' | 'violet' | 'amber'
}) {
  return (
    <div className={`stat ${tone ?? ''}`}>
      <span className="k">{k}</span>
      <span className="v">{v}</span>
    </div>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="field">
      <label>
        {label}
        {hint && <em>{hint}</em>}
      </label>
      {children}
    </div>
  )
}

function ConnPill({ conn, status }: { conn: string; status: Live['status'] }) {
  if (conn !== 'open') {
    return (
      <span className="pill bad">
        <i />
        {conn === 'connecting' ? 'connecting' : 'offline'}
      </span>
    )
  }
  if (status === 'running') {
    return (
      <span className="pill live">
        <i />
        running
      </span>
    )
  }
  if (status === 'paused') {
    return (
      <span className="pill paused">
        <i />
        paused
      </span>
    )
  }
  if (status === 'stopping') {
    return (
      <span className="pill paused">
        <i />
        stopping
      </span>
    )
  }
  return (
    <span className="pill off">
      <i />
      {status === 'idle' ? 'connected' : status}
    </span>
  )
}

function codeClass(code: number) {
  if (!code) return 'none'
  if (code >= 500) return 'bad'
  if (code >= 400) return 'warn'
  return 'ok'
}

function Lanes({ lanes }: { lanes: WorkerLane[] }) {
  if (lanes.length === 0) {
    return (
      <div className="empty">
        <div className="ico">▚</div>
        <p>No active workers. Start a job to see per-worker lanes.</p>
      </div>
    )
  }
  return (
    <div className="lanes">
      {lanes.map((l) => {
        const idx = PHASE_ORDER.indexOf(l.phase)
        const bad = l.phase === 'error'
        return (
          <div
            key={l.workerId}
            className={`lane ${bad ? 'err' : l.phase === 'callback' ? 'ok' : 'active'}`}
          >
            <div className="lane-id">W{l.workerId}</div>
            <div className="lane-body">
              <div className="lane-top">
                <span className="lane-step">{l.step || 'waiting…'}</span>
                <span className="lane-tag">{l.tag}</span>
              </div>
              <div className="steps">
                {PHASE_ORDER.map((p, i) => (
                  <span
                    key={p}
                    className={`seg ${
                      bad ? 'bad' : i < idx ? 'on' : i === idx ? 'on cur' : ''
                    }`}
                  />
                ))}
              </div>
            </div>
            <div className="lane-right">
              <span className="phase-name">{PHASE_LABEL[l.phase as WorkerPhase]}</span>
              <span className={`code ${codeClass(l.statusCode)}`}>
                {l.statusCode || '—'}
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}

function AccountsTable({ rows }: { rows: Account[] }) {
  if (rows.length === 0) {
    return (
      <div className="empty">
        <div className="ico">✉</div>
        <p>No accounts recorded yet.</p>
      </div>
    )
  }
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>email</th>
          <th>password</th>
          <th>result</th>
          <th>detail</th>
          <th>time</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((a, i) => (
          <tr key={`${a.id}-${i}`} className={i === 0 ? 'fresh' : ''}>
            <td className="mono">{a.email || '—'}</td>
            <td className="mono">
              {a.password ? (
                <>
                  {a.password}{' '}
                  <button
                    className="copy"
                    onClick={() => void navigator.clipboard?.writeText(`${a.email}|${a.password}`)}
                  >
                    copy
                  </button>
                </>
              ) : (
                '—'
              )}
            </td>
            <td>
              <span className={`badge ${a.success ? 'ok' : 'bad'}`}>
                {a.success ? 'session ok' : 'failed'}
              </span>
            </td>
            <td>{a.error || ''}</td>
            <td className="num">{fmtClock(a.created_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function statusBadge(s: JobStatus) {
  if (s === 'completed') return 'ok'
  if (s === 'failed' || s === 'stopped') return 'bad'
  if (s === 'paused') return 'amber'
  return 'neutral'
}

function HistoryTable({ rows }: { rows: Job[] }) {
  if (rows.length === 0) {
    return (
      <div className="empty">
        <div className="ico">⌗</div>
        <p>No jobs yet. History persists across restarts.</p>
      </div>
    )
  }
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>job</th>
          <th>target</th>
          <th>ok</th>
          <th>failed</th>
          <th>attempts</th>
          <th>status</th>
          <th>elapsed</th>
          <th>started</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((j) => (
          <tr key={j.id}>
            <td className="mono">{j.id.slice(0, 8)}</td>
            <td className="num">{j.target}</td>
            <td className="num" style={{ color: '#86efac' }}>
              {j.success_count}
            </td>
            <td className="num" style={{ color: '#fca5a5' }}>
              {j.failure_count}
            </td>
            <td className="num">{j.attempts}</td>
            <td>
              <span className={`badge ${statusBadge(j.status)}`}>{j.status}</span>
            </td>
            <td className="num">{fmtDuration(j.elapsed_ms)}</td>
            <td className="num">{new Date(j.started_at).toLocaleString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
