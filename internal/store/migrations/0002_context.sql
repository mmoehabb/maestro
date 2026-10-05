CREATE TABLE native_conversations (
  id INTEGER PRIMARY KEY,
  task_id INTEGER NOT NULL REFERENCES tasks,
  agent TEXT NOT NULL,
  native_id TEXT NOT NULL,
  UNIQUE(task_id, agent, native_id)
);
INSERT INTO native_conversations(task_id, agent, native_id)
SELECT DISTINCT task_id, agent, native_id FROM agent_sessions WHERE COALESCE(native_id,'') != '';
ALTER TABLE agent_sessions ADD COLUMN conversation_id INTEGER REFERENCES native_conversations;
UPDATE agent_sessions SET conversation_id=(SELECT id FROM native_conversations n
 WHERE n.task_id=agent_sessions.task_id AND n.agent=agent_sessions.agent AND n.native_id=agent_sessions.native_id);
ALTER TABLE turns ADD COLUMN conversation_id INTEGER REFERENCES native_conversations;
ALTER TABLE turns ADD COLUMN source_key TEXT;
ALTER TABLE turns ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX turns_source ON turns(conversation_id, source_key);
CREATE TABLE handoffs (
  id INTEGER PRIMARY KEY,
  task_id INTEGER NOT NULL REFERENCES tasks,
  from_session INTEGER REFERENCES agent_sessions,
  to_session INTEGER REFERENCES agent_sessions,
  agent TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  delivered_at INTEGER
);
CREATE INDEX handoffs_task ON handoffs(task_id, id);
CREATE UNIQUE INDEX handoffs_pending ON handoffs(task_id) WHERE delivered_at IS NULL;
