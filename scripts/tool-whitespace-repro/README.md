# Tool argument whitespace reproduction

Run the actual Responses SSE/WebSocket adapters and reliable-stream guard against
local synthetic wire events. No credentials, external model, or tool executor is
used. JSONL output records each case and exits nonzero on an unexpected result.

```bash
go run ./scripts/tool-whitespace-repro -log-dir /tmp/wuu-whitespace > /tmp/wuu-whitespace.jsonl
```

The 14 cases cover runaway JSON formatting, spaces inside function/custom string
arguments, nonempty initial argument prefixes, an empty first custom delta, and
escaped custom source. Successful cases must retain the exact final arguments.
Both nonempty-prefix cases fail on the adapter before the prefix fix: the start
event loses the prefix, so the guard sees string contents outside a JSON string.
The runaway-formatting case must remain blocked after the fix.

The debug log contains metadata only:

- `responses_tool_start`: provider call/item identity, kind, initial argument size.
- `responses_tool_raw_whitespace`: one record per tool when a raw whitespace run
  first reaches 4 KiB, including delta index, byte counts, and chunk fingerprint.
  This is diagnostic evidence, not a validity decision; custom source and JSON
  strings may legitimately contain whitespace.
- `tool_argument_whitespace`: the actual guard rejection, correlated with the
  inference operation/attempt and call/item, with parser state, byte counts,
  character counts, and a normalized chunk fingerprint.

These records do not contain argument contents or require `WUU_DEBUG_WIRE`.
A live incident needs the instrumented core running with its debug log enabled.
A synthetic reproduction proves the boundary defect, not the cause of a previous
incident whose wire events were not captured.
