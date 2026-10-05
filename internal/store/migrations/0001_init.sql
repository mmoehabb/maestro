CREATE TABLE projects (
  id INTEGER PRIMARY KEY, root TEXT NOT NULL UNIQUE, remote TEXT NOT NULL,
  default_branch TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE tasks (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects,
  slug TEXT NOT NULL, title TEXT NOT NULL, goal TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL, base_branch TEXT NOT NULL, worktree TEXT NOT NULL,
  lifecycle TEXT NOT NULL DEFAULT 'new', tab_order INTEGER NOT NULL,
  agent TEXT NOT NULL, prompt TEXT NOT NULL DEFAULT '',
  pr_number INTEGER, pr_url TEXT, pr_state TEXT, ci_state TEXT, review_state TEXT,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, archived_at INTEGER,
  UNIQUE(project_id, slug)
);
CREATE TABLE agent_sessions (
  id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL REFERENCES tasks,
  agent TEXT NOT NULL, native_id TEXT, started_at INTEGER NOT NULL, ended_at INTEGER,
  exit_code INTEGER, handoff_from INTEGER REFERENCES agent_sessions
);
CREATE TABLE turns (
  id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL REFERENCES agent_sessions,
  seq INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL, ts INTEGER NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE TABLE events (
  id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL REFERENCES tasks,
  ts INTEGER NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL
);
CREATE INDEX events_task ON events(task_id, id);
CREATE INDEX sessions_task ON agent_sessions(task_id, id);
