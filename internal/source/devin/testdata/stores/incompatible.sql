CREATE TABLE refinery_schema_history(version int4 PRIMARY KEY, name VARCHAR(255), applied_on VARCHAR(255), checksum VARCHAR(255));
INSERT INTO refinery_schema_history(version,name,applied_on) VALUES (1,'initial_schema','2025-01-01T00:00:00Z'),(5,'message_forest','2025-02-01T00:00:00Z');
CREATE TABLE sessions (id TEXT PRIMARY KEY, working_directory TEXT NOT NULL, backend_type TEXT NOT NULL, model TEXT NOT NULL, agent_mode TEXT NOT NULL, created_at INTEGER NOT NULL, last_activity_at INTEGER NOT NULL, title TEXT, main_chain_id INTEGER);
CREATE TABLE message_nodes (row_id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, node_id INTEGER NOT NULL, parent_node_id INTEGER, chat_message TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(session_id, node_id));
INSERT INTO sessions VALUES ('proto-mole','/home/user/Code/old','windsurf','swe-1','normal',1700000000,1700000001,'Ancient schema session',0);
INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at) VALUES ('proto-mole',0,NULL,'{"message_id":"88888888-0000-4000-8000-000000000001","role":"user","content":"Pre-tool_call_state era."}',1700000000);
