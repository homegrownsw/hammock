<?php
/* Hammock's page asks this for everything it shows or does, and this asks
 * hmk: the page can do nothing the CLI can't (so a server without Unraid
 * does it all with hmk). Answers are JSON. Unraid checks the POST's
 * csrf_token before this runs. */

header('Content-Type: application/json');

$cfg_file = '/boot/config/plugins/hammock/hammock.cfg';
$rc = '/usr/local/emhttp/plugins/hammock/scripts/rc.hammock';
$hmk = '/usr/local/bin/hmk';

function reply($data, $code = 200) {
  http_response_code($code);
  echo json_encode($data);
  exit;
}

// run runs a command (each argument quoted), returning its output and status.
function run($argv) {
  $cmd = implode(' ', array_map('escapeshellarg', $argv)) . ' 2>&1';
  exec($cmd, $out, $status);
  return [implode("\n", $out), $status];
}

// hmk runs hmk with args; with $json, decodes what it prints.
function hmk($args, $json = false) {
  global $hmk;
  [$out, $status] = run(array_merge([$hmk], $args));
  if ($status !== 0) reply(['error' => trim($out) ?: "hmk exited $status"], 500);
  if (!$json) return trim($out);
  $v = json_decode($out, true);
  if ($v === null) reply(['error' => 'hmk said: ' . trim($out)], 500);
  return $v;
}

function cfg() {
  global $cfg_file;
  $c = ['FOLDER' => '/mnt/user/appdata/hammock', 'AUTOSTART' => 'yes', 'PANE_HOME' => 'folder'];
  if (is_readable($cfg_file)) $c = array_merge($c, parse_ini_file($cfg_file) ?: []);
  return $c;
}

// id: the ids hmk gives (a computer's user id, a device's, a link request's)
// and network ids (tailscale, 192.168.1.0/24).
function id_arg($name) {
  $v = $_POST[$name] ?? '';
  if (!is_string($v) || !preg_match('#\A[A-Za-z0-9][A-Za-z0-9_.:/ -]{0,127}\z#', $v)) reply(['error' => "bad $name"], 400);
  return $v;
}

$action = $_POST['action'] ?? '';
switch ($action) {
case 'status':
  [$out, $status] = run([$hmk, 'status', '--json']);
  $st = json_decode($out, true) ?: ['running' => false];
  $st['config'] = cfg();
  $st['array_started'] = (bool)preg_match('/^mdState="STARTED"/m', @file_get_contents('/var/local/emhttp/var.ini') ?: '');
  if (!empty($st['running'])) {
    [$c] = run([$hmk, 'computers', '--json']);
    $st['computers'] = json_decode($c, true)['list'] ?? [];
    [$d] = run([$hmk, 'devices', '--json']);
    $st['devices'] = json_decode($d, true)['devices'] ?? [];
    [$l] = run([$hmk, 'link', 'pending', '--json']);
    $st['link_requests'] = json_decode($l, true) ?: [];
  }
  reply($st);

case 'start':
case 'stop':
case 'restart':
  [$out, $status] = run([$rc, $action]);
  if ($status !== 0) reply(['error' => trim($out)], 500);
  reply(['ok' => true, 'output' => trim($out)]);

case 'save':
  $folder = rtrim($_POST['folder'] ?? '', '/');
  $auto = ($_POST['autostart'] ?? '') === 'yes' ? 'yes' : 'no';
  $pane_home = ($_POST['pane_home'] ?? '') === 'root' ? 'root' : 'folder';
  if (!preg_match('#\A/[A-Za-z0-9_./ -]+\z#', $folder) || strpos($folder, '..') !== false || $folder === '/boot' || strpos($folder, '/boot/') === 0) {
    reply(['error' => 'Choose a folder on a disk, pool or share, such as /mnt/user/appdata/hammock (not on the flash drive).'], 400);
  }
  $old = cfg();
  @mkdir(dirname($cfg_file), 0755, true);
  file_put_contents($cfg_file, "FOLDER=\"$folder\"\nAUTOSTART=\"$auto\"\nPANE_HOME=\"$pane_home\"\n");
  $changed = $old['FOLDER'] !== $folder || $old['PANE_HOME'] !== $pane_home;
  if ($changed) {
    // A new folder: the daemon starts afresh there (the old one's state
    // stays where it was). A new home: new panes get it.
    run([$rc, 'restart']);
  }
  reply(['ok' => true, 'restarted' => $changed]);

case 'remote':
  hmk(['remote', ($_POST['on'] ?? '') === 'yes' ? 'on' : 'off']);
  reply(['ok' => true]);

case 'networks':
  reply(hmk(['remote', 'networks', '--json'], true));

case 'network':
  hmk(['remote', ($_POST['chosen'] ?? '') === 'yes' ? 'enable' : 'disable', id_arg('id')]);
  reply(['ok' => true]);

case 'link_invite':
  // Lets a computer ask to link for ten minutes (it's off Tailscale, or
  // Tailscale can't vouch for it), and gives a link to paste there.
  reply(['link' => hmk(['link', '--invite'])]);

case 'link_confirm':
case 'link_refuse':
  hmk(['link', $action === 'link_confirm' ? 'confirm' : 'refuse', id_arg('id')]);
  reply(['ok' => true]);

case 'unlink':
  hmk(['computers', 'unlink', id_arg('id')]);
  reply(['ok' => true]);

case 'pair':
  reply(hmk(['pair', '--json'], true));

case 'revoke':
  hmk(['devices', 'revoke', id_arg('id')]);
  reply(['ok' => true]);

case 'tui':
  // hmk's TUI in a browser window, through ttyd as Unraid's own terminal
  // windows are (nginx serves /logterminal/NAME/ from /var/tmp/NAME.sock).
  $sock = '/var/tmp/hammock.sock';
  exec('pgrep -f ' . escapeshellarg("ttyd.*$sock"), $pids, $found);
  if ($found === 0) {
    // One already: leave it while a window is open, else start afresh.
    exec('pgrep -P ' . (int)$pids[0], $kids, $busy);
    if ($busy === 0) reply(['url' => '/logterminal/hammock/']);
    exec('kill ' . (int)$pids[0]);
    usleep(200000);
  }
  $script = '/var/tmp/hammock.tui.sh';
  file_put_contents($script, "#!/bin/bash\n. /var/run/hammock.env 2>/dev/null\nexport TERM=xterm-256color\nexec /usr/local/hammock/bin/hmk tui\n");
  chmod($script, 0700);
  exec('ttyd-exec -i ' . escapeshellarg($sock) . ' ' . escapeshellarg($script));
  reply(['url' => '/logterminal/hammock/']);
}

reply(['error' => 'unknown action'], 400);
