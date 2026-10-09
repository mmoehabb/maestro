CREATE TABLE portable_tasks (
  task_id INTEGER PRIMARY KEY REFERENCES tasks ON DELETE CASCADE,
  uuid TEXT NOT NULL,
  manifest TEXT NOT NULL,
  context TEXT NOT NULL,
  fingerprint TEXT NOT NULL DEFAULT '',
  git_state TEXT NOT NULL DEFAULT '',
  fresh INTEGER NOT NULL DEFAULT 0,
  source_ref TEXT NOT NULL DEFAULT '',
  source_commit TEXT NOT NULL DEFAULT ''
);
CREATE TABLE portable_deleted (
  project_id INTEGER NOT NULL REFERENCES projects,
  uuid TEXT NOT NULL,
  PRIMARY KEY(project_id, uuid)
);
