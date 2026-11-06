import { StrictMode, useCallback, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import './style.css'

// ---- API ----------------------------------------------------------------------------------------

async function api(path) {
  const res = await fetch(path, { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  return res.json()
}

async function sendCapture(item) {
  const fd = new FormData()
  fd.set('kind', item.kind)
  fd.set('text', item.text)
  if (item.photo) fd.set('photo', item.photo, item.photoName)
  const res = await fetch('/api/capture', { method: 'POST', body: fd, headers: { 'X-Tether': '1' } })
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  return res.json()
}

// ---- offline queue: captures wait here while the laptop sleeps ---------------------------------
// Text captures survive a reload (localStorage); a photo waits only while the app stays open.

const QKEY = 'tether-queue'
const loadQueue = () => {
  try {
    return JSON.parse(localStorage.getItem(QKEY) || '[]')
  } catch {
    return []
  }
}
const saveQueue = (q) => {
  try {
    localStorage.setItem(QKEY, JSON.stringify(q.filter((i) => !i.photo)))
  } catch {}
}

function useQueue() {
  const [queue, setQueue] = useState(loadQueue)
  const qref = useRef(queue)
  const [status, setStatus] = useState('')
  const set = (q) => {
    qref.current = q
    setQueue(q)
    saveQueue(q)
  }
  const flush = useCallback(async () => {
    for (const item of [...qref.current]) {
      try {
        const { path } = await sendCapture(item)
        set(qref.current.filter((i) => i.id !== item.id))
        setStatus(`Saved to ${path}`)
      } catch (e) {
        setStatus(navigator.onLine ? `Waiting for the laptop: ${e.message}` : 'Offline: saved on this iPad, will send later')
        return
      }
    }
  }, [])
  useEffect(() => {
    flush()
    const t = setInterval(flush, 15000)
    window.addEventListener('online', flush)
    return () => {
      clearInterval(t)
      window.removeEventListener('online', flush)
    }
  }, [flush])
  const add = (item) => {
    set([...qref.current, { ...item, id: crypto.randomUUID() }])
    flush()
  }
  return { queue, add, status }
}

// ---- views --------------------------------------------------------------------------------------

function Capture({ add, queue, status }) {
  const [kind, setKind] = useState('text')
  const [text, setText] = useState('')
  const [photo, setPhoto] = useState(null)
  const submit = (e) => {
    e.preventDefault()
    if (!text.trim() && !photo) return
    add({ kind: photo ? 'photo' : kind, text, photo, photoName: photo?.name })
    setText('')
    setPhoto(null)
    e.target.reset()
  }
  return (
    <form className="capture" onSubmit={submit}>
      <div className="seg" role="radiogroup" aria-label="What are you capturing">
        {['text', 'link'].map((k) => (
          <label key={k} className={kind === k ? 'on' : ''}>
            <input type="radio" name="kind" value={k} checked={kind === k} onChange={() => setKind(k)} />
            {k === 'text' ? 'Note' : 'Link'}
          </label>
        ))}
      </div>
      <label className="sr" htmlFor="capture-text">
        {kind === 'link' ? 'Link' : 'Note'}
      </label>
      <textarea
        id="capture-text"
        rows={kind === 'link' ? 2 : 7}
        placeholder={kind === 'link' ? 'https://…' : 'A thought, a todo, a quote…'}
        value={text}
        onChange={(e) => setText(e.target.value)}
        autoFocus
      />
      <div className="row">
        <label className="file">
          <input id="capture-photo" type="file" accept="image/*" onChange={(e) => setPhoto(e.target.files[0] || null)} />
          {photo ? `Photo: ${photo.name}` : 'Add a photo'}
        </label>
        <button type="submit">Send to inbox</button>
      </div>
      <p className="status" aria-live="polite">
        {queue.length > 0 ? `${queue.length} waiting · ` : ''}
        {status}
      </p>
    </form>
  )
}

function Markdown({ md }) {
  const html = DOMPurify.sanitize(marked.parse(md.replace(/^---\n[\s\S]*?\n---\n/, '')))
  return <article className="md" dangerouslySetInnerHTML={{ __html: html }} />
}

function Reader({ name, title }) {
  const [state, setState] = useState({ loading: true })
  const load = useCallback(() => {
    setState({ loading: true })
    api(name === 'today' ? '/api/today' : `/api/page?name=${encodeURIComponent(name)}`)
      .then((r) => setState({ md: r.markdown }))
      .catch((e) => setState({ error: e.message }))
  }, [name])
  useEffect(load, [load])
  return (
    <section>
      <header className="bar">
        <h2>{title}</h2>
        <button className="ghost" onClick={load}>
          Reload
        </button>
      </header>
      {state.loading && <p className="muted">Loading…</p>}
      {state.error && <p className="error">{state.error === 'not found' ? 'No note for today yet.' : state.error}</p>}
      {state.md && <Markdown md={state.md} />}
    </section>
  )
}

function Search({ open }) {
  const [q, setQ] = useState('')
  const [out, setOut] = useState(null)
  const run = (e) => {
    e.preventDefault()
    if (!q.trim()) return
    setOut({ loading: true })
    api(`/api/search?q=${encodeURIComponent(q)}`)
      .then((r) => setOut({ results: r.results }))
      .catch((err) => setOut({ error: err.message }))
  }
  // search.py prints "score  wiki/path.md — title" then an excerpt line
  const hits = (out?.results || '')
    .split('\n')
    .map((l) => l.match(/^\s*[\d.]+\s+wiki\/(\S+)\.md\s+—\s+(.*)$/))
    .filter(Boolean)
  return (
    <section>
      <form className="search" onSubmit={run}>
        <label className="sr" htmlFor="search-q">
          Search the brain
        </label>
        <input id="search-q" type="search" placeholder="Search the brain" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit">Search</button>
      </form>
      {out?.loading && <p className="muted">Searching…</p>}
      {out?.error && <p className="error">{out.error}</p>}
      <ul className="hits">
        {hits.map(([, path, title]) => (
          <li key={path}>
            <button className="link" onClick={() => open(path, title)}>
              {title}
              <small>{path}</small>
            </button>
          </li>
        ))}
      </ul>
      {out?.results && hits.length === 0 && <p className="muted">Nothing found.</p>}
    </section>
  )
}

const TABS = [
  ['capture', 'Capture'],
  ['today', 'Today'],
  ['board', 'Board'],
  ['search', 'Search'],
]

function App() {
  const [tab, setTab] = useState('capture')
  const [page, setPage] = useState(null)
  const [who, setWho] = useState('')
  const q = useQueue()
  useEffect(() => {
    api('/api/whoami').then((r) => setWho(r.login)).catch(() => {})
    if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {})
  }, [])
  return (
    <div className="app">
      <header className="top">
        <h1>tether</h1>
        <span className="muted">{who || 'not connected'}</span>
      </header>
      <nav className="tabs" aria-label="Views">
        {TABS.map(([id, label]) => (
          <button key={id} className={tab === id && !page ? 'on' : ''} aria-current={tab === id && !page ? 'page' : undefined}
            onClick={() => { setTab(id); setPage(null) }}>
            {label}
          </button>
        ))}
      </nav>
      <main>
        {page ? (
          <>
            <button className="ghost back" onClick={() => setPage(null)}>
              ← Back to search
            </button>
            <Reader name={page.path} title={page.title} />
          </>
        ) : tab === 'capture' ? (
          <Capture {...q} />
        ) : tab === 'today' ? (
          <Reader name="today" title="Today" />
        ) : tab === 'board' ? (
          <Reader name="board" title="Board" />
        ) : (
          <Search open={(path, title) => setPage({ path, title })} />
        )}
      </main>
    </div>
  )
}

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
