# Sandbox commands, files and terminals

Exec starts in the background. `exec watch` pages live, separately tagged stdout
and stderr; follow `next_cursor` until absent to obtain all retained bytes.
`exec get` reports the exit code, signal, per-stream byte counts, timeout and
truncation status. Output is kept in private disk spools rather than the control
journal. Output storage failure is reported explicitly. PTYs combine the terminal
streams, as a normal terminal does.

`exec create` accepts `env`, `cwd`, `stdin_blob_id`, `stdin_open`, `pty`, and
`timeout_ms` (zero means no command deadline). The worker request timeout does
not set the command deadline. `exec interrupt` accepts signals 2, 15 and 9.
`exec input` accepts up to 4096 bytes per call and `eof` for piped stdin;
`exec resize` sets PTY rows and columns. HTTP and MCP use these same operations.
A lost command agent produces an unavailable sandbox and an explicit unknown
result, never a fabricated successful exit.

```sh
lamp sandbox shell sb_example
lamp sandbox shell sb_example --exec-id ex_existing_pty
lamp sandbox cp ./binary sb_example:/tmp/binary
lamp sandbox cp sb_example:/tmp/binary ./binary-return
lamp sandbox file-read sb_example --path '/tmp/☃ space.txt'
lamp sandbox file-list sb_example --path /tmp
lamp sandbox file-stat sb_example --path /tmp/binary
```

PTY shells use the local or SSH-negotiated TERM, with xterm-256color as the
default. Shell reconnect reuses the PTY exec and replays retained output. Disconnecting a
PTY leaves it running; interrupt it explicitly to end it. Guest images must carry
the matching guest helper. The file infrastructure uses Python 3 in the guest.
SDK convenience methods are deferred by the owner; the canonical HTTP and MCP
operations are installed.

File read is binary-safe base64 with an offset, SHA-256 of the returned chunk,
and EOF. Requests return at most 24 KiB; advance by the decoded length. File
list pages up to 100 entries using its cursor. Stat, rename, delete, content/name
search, and polling watch are installed. Paths are normalized inside the guest;
NUL paths are invalid and missing paths return NOT_FOUND. Search returns up to
100 matches; narrow the query if the response sets `truncated`. Watch returns
a snapshot cursor and changed entries; removed entries have type `deleted`.

One-step copy streams through the existing authenticated blob/transfer API,
checks SHA-256, and removes temporary blobs. Regular files support up to 4 GiB;
directory archives retain the existing 256 MiB limit. Uploads replace the target
only after staging the complete content. Downloads publish a local temporary
file only after successful verification.

An enrolled `lamp-access` user can use stock OpenSSH:

```sh
ssh -t -p 2222 owner@fleet shell sb_example
scp -P 2222 ./binary owner@fleet:sb_example:/tmp/binary
scp -P 2222 owner@fleet:sb_example:/tmp/binary ./binary-return
rsync -e 'ssh -p 2222' ./binary owner@fleet:sb_example:/tmp/binary
ssh -N -p 2222 -L 8080:sb_example:8000 owner@fleet
```

Modern scp uses the SFTP subsystem. Paths explicitly select the sandbox; the
access host filesystem is never exposed. Rsync requires rsync in the guest.
Forwarding targets the selected sandbox's loopback through public exec, with
its caller's authorization. It never dials the access host's network.
