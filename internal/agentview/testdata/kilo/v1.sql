-- A Kilo CLI database (kilo.db), in OpenCode 1.x's schema, as
-- Kilo-Org/kilocode (@kilocode/cli 7.8.27) writes it: the tables from
-- packages/core/src/session/sql.ts (only the columns Hammock reads, plus the
-- NOT NULL ones), rows shaped by packages/schema/src/v1/session.ts. A
-- message's data leaves out its id and sessionID, a part's its id,
-- sessionID and messageID (columns); a user's words are in its text parts.
-- Times are unix milliseconds.

CREATE TABLE `session` (
  `id` text PRIMARY KEY,
  `project_id` text NOT NULL,
  `parent_id` text,
  `slug` text NOT NULL,
  `directory` text NOT NULL,
  `title` text NOT NULL,
  `version` text NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL
);
CREATE TABLE `message` (
  `id` text PRIMARY KEY,
  `session_id` text NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL
);
CREATE TABLE `part` (
  `id` text PRIMARY KEY,
  `message_id` text NOT NULL,
  `session_id` text NOT NULL,
  `time_created` integer NOT NULL,
  `time_updated` integer NOT NULL,
  `data` text NOT NULL
);

INSERT INTO `session` VALUES
  ('ses_5a1b2c3d4e5fKiloSession1', 'prj_1', NULL, 'calm-lake', '/Users/me/site', 'New session - 2025-11-07T08:00:00.000Z', '7.8.27', 1762502400000, 1762502460000);

INSERT INTO `message` VALUES
  ('msg_k1', 'ses_5a1b2c3d4e5fKiloSession1', 1762502400000, 1762502400000,
   '{"role":"user","time":{"created":1762502400000},"agent":"code","model":{"providerID":"kilo","modelID":"kilo/auto"}}'),
  ('msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502401000, 1762502430000,
   '{"role":"assistant","time":{"created":1762502401000,"completed":1762502430000},"parentID":"msg_k1","modelID":"anthropic/claude-sonnet-4.5","providerID":"kilo","mode":"code","agent":"code","path":{"cwd":"/Users/me/site","root":"/Users/me/site"},"cost":0.004,"tokens":{"input":900,"output":120,"reasoning":0,"cache":{"read":3000,"write":100}},"finish":"stop"}'),
  ('msg_k3', 'ses_5a1b2c3d4e5fKiloSession1', 1762502440000, 1762502440000,
   '{"role":"user","time":{"created":1762502440000},"agent":"code","model":{"providerID":"kilo","modelID":"kilo/auto"}}'),
  ('msg_k4', 'ses_5a1b2c3d4e5fKiloSession1', 1762502441000, 1762502460000,
   '{"role":"assistant","time":{"created":1762502441000},"error":{"name":"MessageAbortedError","data":{"message":"The operation was aborted."}},"parentID":"msg_k3","modelID":"anthropic/claude-sonnet-4.5","providerID":"kilo","mode":"code","agent":"code","path":{"cwd":"/Users/me/site","root":"/Users/me/site"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}');

INSERT INTO `part` VALUES
  ('prt_k1a', 'msg_k1', 'ses_5a1b2c3d4e5fKiloSession1', 1762502400000, 1762502400000,
   '{"type":"text","text":"Add a footer to index.html"}'),
  ('prt_k1b', 'msg_k1', 'ses_5a1b2c3d4e5fKiloSession1', 1762502400000, 1762502400000,
   '{"type":"text","text":"<environment_details>…</environment_details>","synthetic":true}'),
  ('prt_k2a', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502401000, 1762502401000,
   '{"type":"step-start","snapshot":"4b825dc"}'),
  ('prt_k2b', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502402000, 1762502403000,
   '{"type":"reasoning","text":"Look at the page first.","time":{"start":1762502402000,"end":1762502403000}}'),
  ('prt_k2c', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502404000, 1762502405000,
   '{"type":"tool","callID":"toolu_1","tool":"bash","state":{"status":"completed","input":{"command":"ls","description":"List the site''s files"},"output":"index.html\nstyle.css\n","title":"ls","metadata":{"exit":0},"time":{"start":1762502404000,"end":1762502405000}}}'),
  ('prt_k2d', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502406000, 1762502407000,
   '{"type":"tool","callID":"toolu_2","tool":"write","state":{"status":"completed","input":{"filePath":"/Users/me/site/footer.html","content":"<footer>\n  © 2025\n</footer>"},"output":"Wrote file successfully.","title":"footer.html","metadata":{},"time":{"start":1762502406000,"end":1762502407000}}}'),
  ('prt_k2e', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502408000, 1762502409000,
   '{"type":"tool","callID":"toolu_3","tool":"question","state":{"status":"completed","input":{"questions":[{"question":"Which year should the footer show?","header":"Year","options":[{"label":"2025","description":"This year"},{"label":"Auto","description":"Work it out in JavaScript"}]}]},"output":"User answered: 2025","title":"Asked 1 question","metadata":{},"time":{"start":1762502408000,"end":1762502409000}}}'),
  ('prt_k2f', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502429000, 1762502430000,
   '{"type":"text","text":"Added footer.html.","time":{"start":1762502429000,"end":1762502430000}}'),
  ('prt_k2g', 'msg_k2', 'ses_5a1b2c3d4e5fKiloSession1', 1762502430000, 1762502430000,
   '{"type":"step-finish","reason":"stop","cost":0.004,"tokens":{"input":900,"output":120,"reasoning":0,"cache":{"read":3000,"write":100}}}'),
  ('prt_k3a', 'msg_k3', 'ses_5a1b2c3d4e5fKiloSession1', 1762502440000, 1762502440000,
   '{"type":"text","text":"Now style it"}'),
  ('prt_k4a', 'msg_k4', 'ses_5a1b2c3d4e5fKiloSession1', 1762502442000, 1762502443000,
   '{"type":"tool","callID":"toolu_4","tool":"edit","state":{"status":"pending","input":{},"raw":""}}');
