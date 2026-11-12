# A made-up session spec (an array of {user|say|tool|result, dt}) as a
# Claude Code transcript, one line each, for scripts/agentreplay.
. as $spec
| reduce range(0; $spec | length) as $i ({t: 1794211200, out: [], tool: null};
    ($spec[$i]) as $e
    | .t += ($e.dt // 2)
    | (if $e.tool then "toolu_\($i)" else .tool end) as $tid
    | .out += [{
        type: (if $e.user or $e.result then "user" else "assistant" end),
        uuid: "00000000-0000-4000-8000-\("000000000000\($i)" | .[-12:])",
        parentUuid: (if $i == 0 then null else "00000000-0000-4000-8000-\("000000000000\($i - 1)" | .[-12:])" end),
        timestamp: ((.t | floor | todate | rtrimstr("Z")) + "." + ((.t * 1000 | round) % 1000 + 1000 | tostring | .[1:]) + "Z"),
        sessionId: "demo",
        cwd: $cwd,
        isSidechain: false,
        message: (
          if $e.user then {role: "user", content: $e.user}
          elif $e.result then {role: "user", content: [{type: "tool_result", tool_use_id: .tool, content: $e.result}]}
          elif $e.tool then {role: "assistant", model: "claude-opus-5-5", content: [{type: "tool_use", id: $tid, name: $e.tool, input: $e.input}]}
          else {role: "assistant", model: "claude-opus-5-5", content: [{type: "text", text: $e.say}]} end)
      }]
    | .tool = $tid)
| .out[]
