# Annotated kranz.yaml

One file using every part of the format, with a comment on each field. Nothing
here is required except `project` and one service or action group — copy the
parts you need and delete the rest.

For field types, defaults, and validation rules, see the
[configuration reference](./configuration).

This page includes `examples/reference/kranz.yaml` from the repository, and a
test loads and validates that file, so what you read here always parses.

<<< @/../examples/reference/kranz.yaml{yaml}

## The three-way status contract

The `remote-stack` probe above uses the default: exit `0` is running, anything
else is stopped. Declare both code sets when your probe can distinguish "not
running" from "I could not find out":

```yaml
status:
  type: command
  # 0 = running, 3 = stopped, anything else = the host did not answer
  command: ssh build-host ./stack-status.sh
  running_exit_codes: [0]
  stopped_exit_codes: [3]
  failure_threshold: 2      # Two unclassified probes in a row → unknown
```

Without `stopped_exit_codes`, an unreachable host would be reported as stopped,
which is a claim Kranz has no evidence for. With it, the service goes `unknown`
and Kranz stops guessing.

## Layering personal overrides

Keep the shared file in version control and your own tweaks out of it:

```bash
kranz -f kranz.yaml -f kranz.local.yaml
```

```yaml
# kranz.local.yaml — only the differences
services:
  api:
    env:
      LOG_LEVEL: debug
    lifecycle:
      start:
        confirm: true       # Overrides only this, keeping the command above.
```
