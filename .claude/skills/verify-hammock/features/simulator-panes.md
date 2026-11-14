# Simulator panes

An iOS simulator in a pane, beside the code being built for it. People tap it in the Mac window. Agents boot it, install and launch apps, open URLs, tap, swipe, type, press buttons, rotate, read its log and screenshot it, all through MCP.

## Sub-features

- `sim-open`: boot a simulator into a pane (`hmk sim`, `sim_open`).
- `sim-screenshot`: its screen in points (`sim_screenshot`, `hmk sim shot`).
- `sim-input`: tap, swipe, type, pinch, buttons, rotate, fold (`sim_tap`, `sim_swipe`, `sim_type`, `sim_press_button`…).
- `sim-apps`: install, launch, terminate (`sim_install_app`, `sim_launch_app`, `sim_terminate_app`).
- `sim-url`: open a web page or deep link (`sim_open_url`).
- `sim-logs`: its os_log (`sim_logs`, `hmk log sim`).
- `sim-shutdown`: close the pane and shut it down (`sim_shutdown`).

## How to get to it (user POV)

- CLI: `hmk sim [DEVICE] [--group G]`, `hmk sim list|shot|tap|type|button|shutdown`.
- MCP: `sim_*` tools.
- Mac: File ▸ New Simulator Tab, the palette.

## Driving it with verify-hammock

Preconditions:

- Baseline run (the daemon runs the simulator, so no app window is needed). Xcode with an iOS runtime.
- **A simulator of your own**, never one of the person's. Create one and register it so `down.sh` deletes it:
  `U=$(xcrun simctl create verify-hammock-sim com.apple.CoreSimulator.SimDeviceType.iPhone-18-Pro com.apple.CoreSimulator.SimRuntime.iOS-27-2) && echo "$U" >"$RUN/sim.udid"` (check `xcrun simctl list runtimes` and `devicetypes` for current names).
- A page for it to show, from a local server registered for cleanup (the simulator shares the Mac's network, but won't open `data:` URLs):
  `mkdir -p "$SCRATCH/site"; printf '<meta name=viewport content="width=device-width"><h1 style="font-size:64px">verify-sim-42</h1>' >"$SCRATCH/site/index.html"; port=$((20000 + RANDOM % 20000)); python3 -m http.server $port --bind 127.0.0.1 --directory "$SCRATCH/site" >"$RUN/http.log" 2>&1 & echo $! >"$RUN/http.pid"`

- **Open.** `$S/mcp.sh "$RUN" 01-open sim_open '{"device":"'$U'","new_tab":true}'` returns `pane_id`, `"device":"verify-hammock-sim"`, `"status":"running"`. Keep the pane id in `P`. Give a fresh device about 20 s to finish booting.
- **Open the page.** `$S/mcp.sh "$RUN" 02-url sim_open_url '{"pane_id":"'$P'","url":"http://127.0.0.1:'$port'/"}'` prints `Opened http://127.0.0.1:…/`. `$RUN/http.log` gets `"GET / HTTP/1.1" 200`.
- **See it.** Wait about 10 s (Safari's first launch on a new device shows its Start Page first), then `$S/mcp.sh "$RUN" 03-shot sim_screenshot '{"pane_id":"'$P'"}'` saves `03-shot-1-0.png`. Look at it: Safari shows **verify-sim-42**. If it still shows the Start Page, wait and screenshot again.
- **Input.** `$S/mcp.sh "$RUN" 04-home sim_press_button '{"pane_id":"'$P'","button":"home"}' sim_screenshot '{"pane_id":"'$P'"}'` prints `Pressed home`, and the screenshot shows the home screen.
- **Logs.** `sim_logs '{"pane_id":"'$P'","last":"30s"}'` prints recent os_log lines (`… Df PosterBoard[…] …`).
- **Shut down.** `$S/mcp.sh "$RUN" 05-down sim_shutdown '{"pane_id":"'$P'"}'` prints `Shut the simulator down and closed its pane`. `down.sh` then deletes the device (`sim.udid`) and stops the server (`http.pid`).

## Gotchas

- `sim_open_url` with a `data:` URL fails: `simctl openurl: exit status 115 … LSApplicationWorkspaceErrorDomain`. Serve the page.
- Screenshots right after opening a URL often catch Safari still loading or on its Start Page. Poll with screenshots rather than one fixed wait.
- `sim_open` with no device picks one of the person's simulators. Always create and pass your own UDID.
- The simulator's content doesn't render in `snapshot.sh` PNGs (it's a live layer). `sim_screenshot` is the proof.
- Booting and Safari's first launch are slow. Don't treat a blank first screenshot as a failure.
