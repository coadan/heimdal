# Agent efficiency measurements

Paired measurements on 2026-07-21 compared agents completing the same tasks
against the same small React workspace with identical model settings and
preinstalled dependencies. Every result passed tests, builds, diff review, and
browser verification.

| Task | Tool | Browser rounds | Commands | Time | Input tokens | Output tokens |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Theme feature | Playwright CLI | 6 | 12 | 275.1 s | 826,995 | 10,536 |
| Theme feature | Heimdal | 3 | 4 | 224.4 s | 600,828 | 9,418 |
| Responsive design | Playwright CLI | 20 | 20 | 275.8 s | 1,243,463 | 9,022 |
| Responsive design | Heimdal | 9 | 9 | 220.6 s | 878,756 | 6,630 |

A browser round is one agent shell turn containing browser work. A composite
Heimdal command may perform several Playwright operations. Help-only commands
are excluded.

These are two agent pairs, not a general performance guarantee. Token totals
include cached context, and agent choices vary. The supported conclusion is
that combined semantic, action, and layout evidence can reduce browser
back-and-forth for these tasks.

Focused local measurements:

- `measure --viewport` averaged 0.71 seconds versus 1.11 seconds for separate
  resize and measure commands across six runs.
- cached session discovery took about 32 µs versus 12 ms for full project
  rediscovery in a deterministic microbenchmark.

The focused measurements cover Heimdal overhead only. Browser startup and
application behavior remain project-dependent.

