# Command help execution

`--command-help off` (the default) and `static` never execute project code.
`execute` explicitly runs trusted project code using `go run ... --help`.
A copied worktree is not a sandbox: project code can access files and the network
with the caller's permissions. Use execution only with projects you trust.

The `command_help` configuration group limits each invocation (10 seconds), the
whole traversal (60 seconds), invocation count (128), depth (8), combined output
per invocation (1 MiB), and total output (8 MiB). Caller cancellation wins.
A timeout, traversal limit, or output overflow terminates the owned process tree
and reports incomplete/error provenance rather than parsing truncated help.

Execution inherits only the OS essentials, Go paths/routing, and validated network
variables listed in embedded `commandhelp-env.yaml`. `GOENV=off` and `GOWORK=off`
are forced. Flags, injection variables, and provider credentials are excluded.
Rejected values are omitted without logging. Proxy credentials, query strings,
fragments, and newline-containing values are rejected. Windows environment names
are matched case-insensitively, deduplicated by the last occurrence, emitted with
canonical names, and sorted.

Outgoing Git uses `outgoing_git` limits: a single 30-second operation deadline,
16 MiB raw stdout, 4 KiB batch headers including their delimiters, and 4 KiB retained
stderr with excess drained and marked. Git blob bodies are consumed incrementally,
retaining at most the existing 64 KiB sample. Existing configuration files need no
rewrite: omitted or zero limits resolve to embedded defaults; negatives fail.

Owned Unix processes use process groups. Windows processes are created suspended,
assigned to a kill-on-close Job Object, then resumed. Failed assignment terminates
and reaps the suspended child. Native Windows containment evidence requires running
the platform fixtures on Windows; cross-compilation does not prove containment.
