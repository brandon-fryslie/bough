# Contributing

Thanks for looking. bough is early and the rough edges are real, so bug reports
and questions are as useful as code right now.

## Getting set up

```
go build ./cmd/bough
go test ./...
```

Two optional extras. The layout test runs the drawing code under Node and
skips itself if Node is missing, so install it if you are touching the web
view. Regenerating the artwork and the embedded fonts needs Python and
fontTools, but building never does.

Driving a real browser is never assumed, because it opens a window and needs
the browser's driver. `go test ./internal/webdriver -browsers=chrome,safari`
asks for it: Chrome needs the chromedriver matching its version on `PATH`, and
Safari needs Settings > Developer > Allow remote automation turned on. Safari's
driver drops most of the input it is sent, so in Safari only that the page
draws frames is checked, and the input checks are skipped with that reason.
`go test ./internal/scenario -browsers=chrome,safari` plays every scenario, the
gestures worth measuring, on a synthetic history; in Safari the page is read
for where the gestures would land, and playing is skipped for the same reason.

`make perf` measures how smoothly the page draws. It plays every scenario five
times in every browser whose driver is installed, on a synthetic history of
`SIZE=small`, `medium` (the default) or `large`, and prints for each the
median, 95th percentile and worst frame and how many frames were missed, judged
against the display's own refresh rate. Input is shown as it arrived, since a
driver can stretch a gesture it was asked to play quickly. Every recording and
its summary are kept in `perf-results/`, with the commit measured, and it exits
non-zero when any run fails or nothing was measured.
`go run ./cmd/boughperf -h` lists the rest, including `-graph` to measure a
graph written by `bough --json`.

One run is noisy, so claim a change by comparing repeated runs taken on the
same machine: `go run ./cmd/boughperf compare before.json after.json` shows
each metric as the middle of its runs with the least and most any run
reached. It calls a change better or worse only when every run after is past
every run before, which noise alone does less than once in a hundred
comparisons, so it needs five runs a side or more on one side for fewer on
the other, and only when a frame time
moved by more than half a frame, since frame times jitter by less than that.
It refuses runs of different graphs.

Run `make lint test` before opening a pull request. CI runs the same commands
on Linux, macOS and Windows.

## What is in scope right now

Claude Code and OpenAI Codex CLI, and only those two.

That is a deliberate limit rather than a queue. Adding the second agent found
real defects in the first, and it turned up a class of problem a single agent
could never have shown: work handed to a sub-agent, replay that spans files
rather than sitting inside one, and two token streams reporting the same
figures. Those were worth finding, and none of them would have been found by
adding a third agent instead.

More agents will come once these two are solid. Until then a pull request
adding a new agent is likely to be turned down, however good the code is,
because every agent has to be maintained against a format that moves and
checked against a real corpus somebody here can see. Plumbing that makes the
seam better, or fixes to the two agents that exist, are very welcome.

If you want a particular agent supported, open an issue saying so. Knowing what
people actually use is more useful than a parser nobody can verify.

## Four rules that hold the design together

Break any of these and the project stops being what it is.

1. **The core never imports an agent package.** `segment`, `rollup` and
   `metrics` see normalised turns and nothing else. That is what makes a
   second agent cheap. Enforced by `TestCoreDoesNotDependOnAnyAgent`.
2. **The graph carries no presentation.** No coordinates, no colours, no
   sizes. The renderer works out its own layout every time. Enforced by
   `TestGraphCarriesNoPresentation`.
3. **The page fetches nothing.** Fonts, artwork and the graph are all embedded.
   A page that phones home would contradict the one promise this makes.
   Enforced by `TestPageFetchesNothingExternal`.
4. **Agent data directories are read only.** bough opens files under
   `~/.claude` and `~/.codex` and never writes there. No test enforces this
   yet, so it is on reviewers to notice.

## Never commit real history

Transcripts contain whatever you typed, which includes pasted keys and
customer names. Nothing in the repo is cut from a real session. The Claude
fixtures keep the record structure with every prompt replaced by a
`text-<hash>` placeholder; the Codex one is written by hand. Anything new goes
the same way, and writing a fixture by hand is usually easier than sanitising
one: a few lines of JSON carrying the single thing the test pins.

## Commits and pull requests

Say what changed and why, in the present tense. If a number appears in the
message it should be one you measured. No attribution trailers.

Small pull requests get read faster than large ones. If you are planning
something big, open an issue first so you do not build the wrong thing.
