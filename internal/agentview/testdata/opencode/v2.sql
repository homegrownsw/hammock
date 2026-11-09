-- An OpenCode 2.x database (opencode.db), as anomalyco/opencode v2.2.5
-- writes it: the session tables from packages/core/src/session/sql.ts (only
-- the columns Hammock reads, plus the NOT NULL ones), rows shaped by
-- packages/schema/src/session-message.ts. A message's data leaves out its
-- id and type (columns); a reply's content is in session_part (2.2 on), or
-- inline in its data as before. Times are unix milliseconds.

CREATE TABLE `session_v2` (
  `id` text PRIMARY KEY,
  `project_id` text NOT NULL,
  `parent_id` text,
  `slug` text NOT NULL,
  `directory` text NOT NULL,
  `path` text,
  `title` text,
  `version` text NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL
);
CREATE TABLE `session_message` (
  `id` text PRIMARY KEY,
  `session_id` text NOT NULL,
  `type` text NOT NULL,
  `seq` integer NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL
);
CREATE TABLE `session_part` (
  `message_id` text NOT NULL,
  `session_id` text NOT NULL,
  `ordinal` integer NOT NULL,
  `type` text NOT NULL,
  `call_id` text,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL,
  PRIMARY KEY(`message_id`, `ordinal`)
);
-- V2 keeps a 1.x database's tables beside its own while it imports them.
CREATE TABLE `session` (`id` text PRIMARY KEY, `parent_id` text, `directory` text, `title` text, `time_created` integer, `time_updated` integer);
CREATE TABLE `message` (`id` text PRIMARY KEY, `session_id` text, `time_created` integer, `time_updated` integer, `data` text);
CREATE TABLE `part` (`id` text PRIMARY KEY, `message_id` text, `session_id` text, `time_created` integer, `time_updated` integer, `data` text);
INSERT INTO `session` VALUES ('ses_0ld1x', NULL, '/Users/me/proj', 'Imported', 1762400000000, 1762400000000);

INSERT INTO `session_v2` VALUES
  ('ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'prj_1', NULL, 'brave-falcon', '/Users/me/proj', NULL, 'Fix the login check', '2.2.5', 1762500000000, 1762500090000),
  ('ses_7f3a9c2e1d4cSubagent0001', 'prj_1', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'quiet-otter', '/Users/me/proj', NULL, NULL, '2.2.5', 1762500030000, 1762500040000),
  ('ses_7f3a9c2e1d4dOtherFolder1', 'prj_2', NULL, 'red-kite', '/Users/me/other', NULL, NULL, '2.2.5', 1762500100000, 1762500100000);

INSERT INTO `session_message` VALUES
  ('msg_01', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'model-switched', 1, 1762500000000, 1762500000000,
   '{"time":{"created":1762500000000},"model":{"id":"claude-sonnet-4-5","providerID":"anthropic"}}'),
  ('msg_02', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'user', 2, 1762500001000, 1762500001000,
   '{"time":{"created":1762500001000},"text":"The login check in /Users/me/proj/auth.go is inverted. Fix it?","files":[],"agents":[],"skills":[]}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'assistant', 3, 1762500002000, 1762500020000,
   '{"time":{"created":1762500002000,"completed":1762500020000},"agent":"build","model":{"id":"claude-sonnet-4-5","providerID":"anthropic"},"content":[],"finish":"stop","cost":0.012,"tokens":{"input":1200,"output":300,"reasoning":40,"cache":{"read":5000,"write":0}}}'),
  ('msg_04', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'shell', 4, 1762500030000, 1762500031000,
   '{"time":{"created":1762500030000,"completed":1762500031000},"shellID":"shl_1","command":"git status --short","status":"exited","exit":0,"output":{"output":" M auth.go\n","cursor":10,"size":10,"truncated":false}}'),
  ('msg_05', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'user', 5, 1762500040000, 1762500040000,
   '{"time":{"created":1762500040000},"text":"Now run the tests","files":[],"agents":[],"skills":[]}'),
  ('msg_06', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'assistant', 6, 1762500041000, 1762500050000,
   '{"time":{"created":1762500041000},"agent":"build","model":{"id":"claude-sonnet-4-5","providerID":"anthropic"},"content":[{"type":"text","text":"Running them now."},{"type":"tool","id":"call_4","name":"shell","state":{"status":"running","input":{"command":"go test ./...","description":"Run the tests"},"metadata":{}},"time":{"created":1762500042000,"ran":1762500042000}}],"tokens":{"input":1500,"output":20,"reasoning":0,"cache":{"read":6200,"write":0}}}'),
  ('msg_07', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 'idle', 7, 1762500090000, 1762500090000,
   '{"time":{"created":1762500090000},"outcome":"interrupted"}'),
  ('msg_08', 'ses_7f3a9c2e1d4cSubagent0001', 'user', 1, 1762500030000, 1762500030000,
   '{"time":{"created":1762500030000},"text":"Find the login tests","files":[],"agents":[],"skills":[]}'),
  ('msg_09', 'ses_7f3a9c2e1d4dOtherFolder1', 'user', 1, 1762500100000, 1762500100000,
   '{"time":{"created":1762500100000},"text":"Hello","files":[],"agents":[],"skills":[]}');

INSERT INTO `session_part` VALUES
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 0, 'reasoning', NULL, 1762500002000, 1762500003000,
   '{"type":"reasoning","text":"The condition uses != where it means ==.","time":{"created":1762500002000,"completed":1762500003000}}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 1, 'tool', 'call_1', 1762500003000, 1762500004000,
   '{"type":"tool","id":"call_1","name":"read","executed":true,"state":{"status":"completed","input":{"path":"auth.go"},"content":[{"type":"text","text":"package auth\n\nfunc ok(t string) bool { return t != want }"}]},"time":{"created":1762500003000,"completed":1762500004000}}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 2, 'tool', 'call_2', 1762500005000, 1762500006000,
   '{"type":"tool","id":"call_2","name":"edit","executed":true,"state":{"status":"completed","input":{"path":"auth.go","oldString":"return t != want","newString":"return t == want"},"content":[{"type":"text","text":"Edited auth.go"}]},"time":{"created":1762500005000,"completed":1762500006000}}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 3, 'tool', 'call_3', 1762500007000, 1762500010000,
   '{"type":"tool","id":"call_3","name":"shell","executed":true,"state":{"status":"error","input":{"command":"go vet ./..."},"error":{"type":"tool","message":"exit status 1: vet: auth.go:3: unreachable code"}},"time":{"created":1762500007000,"completed":1762500010000}}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 4, 'tool', 'call_5', 1762500011000, 1762500012000,
   '{"type":"tool","id":"call_5","name":"patch","executed":true,"state":{"status":"completed","input":{"patchText":"*** Begin Patch\n*** Update File: auth.go\n@@\n-// TODO\n+// ok compares a token with the one wanted.\n*** End Patch"},"content":[{"type":"text","text":"Applied"}]},"time":{"created":1762500011000,"completed":1762500012000}}'),
  ('msg_03', 'ses_7f3a9c2e1d4bAbCdEfGhIjKl', 5, 'text', NULL, 1762500019000, 1762500020000,
   '{"type":"text","text":"Fixed: `ok` now compares with `==`."}');
