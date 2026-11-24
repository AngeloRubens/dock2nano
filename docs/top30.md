# Top 30 Dockerfiles, converted unmodified

## Selection

Source: GitHub stars, read with `gh search repos` (topics self-hosted, docker, web-server, database, and the top of the list by stars) and then with GraphQL, one query per batch of candidates, for a Dockerfile at the root of the default branch. The 30 below are the candidates that have a root Dockerfile, an EXPOSE line or a port the program is known to listen on, and a build that is not GPU, privileged, Docker-in-Docker, or known to be long (judged from the Dockerfile, not measured). Every Dockerfile is used as it is at the commit below: nothing is edited, no build argument is set, no environment variable is given.

Not in the list, and why: most of the most-starred repositories (documentation, agent projects, language trees) have no Dockerfile at the root. Several popular servers (caddy, redis, valkey, dragonfly, strapi, ghost, pocketbase, mkdocs, adminer, phpmyadmin, searxng, coder/code-server, gotify) have none at the root either. Excluded for build size or privileges without a measurement: meilisearch, ferron, vaultwarden, navidrome, chroma, influxdb, siyuan, khoj, netdata, n8n, open-webui, ollama, mastodon, stirling-pdf, consul (its Dockerfile has several targets and copies prebuilt binaries).

The list has one database (tile38) and one key-value store (etcd) among 30; no other database has a root Dockerfile among the candidates looked at (kvrocks, dgraph and cockroach were looked at and not chosen: no EXPOSE line, a long C++ or Go build, or no root Dockerfile).

## The rows

Columns: service name (the --name of the conversion), repository, commit (the default branch HEAD read on the day), Dockerfile path (always the root), probed port, probe kind, and what the program is. The Dockerfile notes are read from the file; they are hypotheses, not measurements.

| service | repository | commit | Dockerfile | port | probe | program | notes read from the Dockerfile (not measured) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| whoami | traefik/whoami | 1ce75d01b697 | Dockerfile | 80 | http | web server (Go, tiny) | multi-stage Go build, FROM scratch, binary copied: expected to build and to answer on 80 |
| httpbin | postmanlabs/httpbin | 2c4572bb8272 | Dockerfile | 8080 | http | HTTP API test server (Python) | gunicorn on 0.0.0.0:8080 in CMD: expected to answer |
| echo-server | jmalloc/echo-server | 332dd6e8153b | Dockerfile | 8080 | http | HTTP echo API (Go) | COPY of a prebuilt binary artifacts/build/... that is not in the repository: expected missing file |
| datasette | simonw/datasette | 775e8ba28525 | Dockerfile | 8001 | http | web app, SQLite browser (Python) | CMD datasette with no host: default bind may be 127.0.0.1 inside the container: expected to be unreachable from outside |
| kanboard | kanboard/kanboard | 58a260505821 | Dockerfile | 80 | http | web app, project board (PHP) | alpine base, entrypoint script; EXPOSE 80 443 |
| ttyd | tsl0922/ttyd | 2922cb89f518 | Dockerfile | 7681 | http | CLI that listens: web terminal (C) | COPY of ./dist/${TARGETARCH}/ttyd, a prebuilt binary not in the repository: expected missing file |
| gitea | go-gitea/gitea | b1a6cdadc83e | Dockerfile | 3000 | http | web app, git server (Go) | 3-stage build including frontend (pnpm, make): long; the port is 3000, SSH 22 also exposed |
| gogs | gogs/gogs | c2380640f6f0 | Dockerfile | 3000 | http | web app, git server (Go) | node and go build stages; ENTRYPOINT is docker/start.sh |
| filebrowser | filebrowser/filebrowser | 833d908884d5 | Dockerfile | 80 | http | web app, file manager (Go) | COPY filebrowser /bin/filebrowser, a prebuilt binary: expected missing file |
| changedetection | dgtlmoon/changedetection.io | d29009b3478b | Dockerfile | 5000 | http | web app, page change monitor (Python) | pip install into a builder stage; ENTRYPOINT docker-entrypoint.sh |
| dashy | lissy93/dashy | ea81d6cbdcb5 | Dockerfile | 8080 | http | dashboard (Node) | yarn build in a builder stage; node server.js |
| homepage | gethomepage/homepage | a1e3a5fd26d6 | Dockerfile | 3000 | http | dashboard (Node) | EXPOSE $PORT with ENV PORT=3000: the port is taken from the ENV, not a literal |
| verdaccio | verdaccio/verdaccio | 13e5bc79fefc | Dockerfile | 4873 | http | package registry (Node) | EXPOSE $VERDACCIO_PORT, VERDACCIO_PORT=4873 in ENV; CMD reads it through a shell |
| http-echo | mendhak/docker-http-https-echo | 184fd2ab87a9 | Dockerfile | 8080 | http | HTTP echo API (Node) | EXPOSE $HTTP_PORT $HTTPS_PORT with ENV HTTP_PORT=8080: probe is the first port |
| shlink | shlinkio/shlink | 80599b89ff9b | Dockerfile | 8080 | http | URL shortener API (PHP) | ENTRYPOINT docker-entrypoint.sh; may need a database or an init step |
| directus | directus/directus | 4856582eec1e | Dockerfile | 8055 | http | CMS and API (Node) | no environment given: Directus needs KEY, SECRET and admin settings, so the expected result is a start failure |
| ntfy | binwiederhier/ntfy | 889b6f071acd | Dockerfile | 80 | http | push notification server (Go) | COPY ntfy /usr/bin, a prebuilt binary: expected missing file |
| mailpit | axllent/mailpit | d54514ceb52c | Dockerfile | 8025 | http | mail server, web UI on 8025 (Go) | EXPOSE 1025 1110 8025: the probe is the web UI on 8025 |
| coredns | coredns/coredns | a0606842e8fa | Dockerfile | 53 | tcp | DNS server (Go) | COPY coredns /coredns, a prebuilt binary: expected missing file; the probe is TCP 53 |
| prometheus | prometheus/prometheus | ad8f26eed18a | Dockerfile | 9090 | http | monitoring server (Go) | COPY .build/${OS}-${ARCH}/prometheus: a build output, not in the repository: expected missing file |
| etcd | etcd-io/etcd | 1f940dc7ecd1 | Dockerfile | 2379 | tcp | key-value database (Go) | no COPY of a binary in the Dockerfile read: CMD /usr/local/bin/etcd may not exist: expected start failure; probe is TCP 2379 |
| syncthing | syncthing/syncthing | 992ae894a44d | Dockerfile | 8384 | http | file sync app (Go) | builder stage with a Go build; the program listens on 8384 |
| wallos | ellite/Wallos | 6e43a0d81997 | Dockerfile | 80 | http | web app, subscriptions (PHP) | php-fpm with nginx.conf, cron and startup.sh |
| excalidraw | excalidraw/excalidraw | 542ad76fa696 | Dockerfile | 80 | http | static web app behind nginx (Node build) | yarn build:app:docker in the build stage; the output is served by nginx |
| tile38 | tidwall/tile38 | 34f86375d07f | Dockerfile | 9851 | tcp | geo database (Go) | FROM alpine and CMD tile38-server: no COPY or install of the binary is in the Dockerfile read: expected start failure; probe is TCP 9851 |
| redlib | redlib-org/redlib | ba116c1186f6 | Dockerfile | 8080 | http | web frontend (Rust) | FROM alpine and CMD redlib: no COPY of the binary in the Dockerfile read: expected start failure |
| homarr | ajnart/homarr | c5873c6e4811 | Dockerfile | 7575 | http | dashboard (Node) | COPY of .next/standalone, a build output of the repository that must exist before the build: expected missing file |
| glance | glanceapp/glance | 53aec7f392ca | Dockerfile | 8080 | http | dashboard (Go) | go build in the builder stage: expected to build; config file given in ENTRYPOINT |
| linkwarden | linkwarden/linkwarden | 24adcc146ba3 | Dockerfile | 3000 | http | web app that needs a database (Node) | needs a PostgreSQL database and a secret: expected start or first-request failure without them |
| reactive-resume | reactive-resume/reactive-resume | 897bcf8e740a | Dockerfile | 3000 | tcp | web app that needs a database (Node) | needs a PostgreSQL database and secrets: expected failure without them; probe is TCP 3000 |

Base of the branch: check/top30 is feat/barenames at 5677ee9 (which contains feat/modules at 35e8082, the --dockerfile mode) plus the workflow and this file. The tool is built in each job from that checkout.

## Method

The workflow is .github/workflows/top30.yml on branch check/top30 (fork AngeloRubens/dock2nano). It has one job per row, fail-fast off, and runs on a push to a check/** branch and by hand. Each job prints its commits: "ROW dock2nano commit" (the checkout) and "ROW ops commit" (the ops build).

Ops: built from nanovms/ops 2177c549df3fab2662d6386042c74bec859275bd, the pin of compose-up.yml, with the nightly nanos kernel copied in as compose-up.yml does. The fork branches feat/from-docker-trace and feat/from-dockerfile (AngeloRubens/ops) are not on top of that pin (diverged: 10 behind and 10 ahead, and 10 behind and 94 ahead), and the tool does not call from-docker: it observes with its own code (internal/observe, through docker), so no fork build is used.

Each row, following the instructions of the tool and of docs/ only:

1. Clones the repository at the pinned commit into a directory named after the row, so that the default name of the conversion (the name of the context directory) is the row name. Nothing is edited.
2. Builds the image with docker build (timed in the job; the conversion builds it again).
3. Runs convert --dockerfile PATH -o DIR --ops-home OPS_HOME --pull, the flags of the usage text. The default observation time (30 seconds) is used; no --command, --with, --exercise or --name flag.
4. Runs the printed next-step lines exactly as printed: the ops pkg get line (once), then the ops compose up line. docs/limits.md says ops compose up needs retries, so up to three tries are made, with ops compose down between them.
5. Runs each printed post-up command ("then, once X is up: ...") as printed, if the report prints one.
6. Waits 60 seconds, reads the address from ops instance logs, and probes with the strict probe of compose-up.yml: 24 tries, 10 seconds apart; 2xx or 3xx, or an open TCP port, is green; 4xx and 5xx are "reached, not a success"; no answer is red.

Every job prints lines that start with ROW: the record of the table. A failed row prints its first error line as "ROW first error".

Rules: no Dockerfile edited, no environment given, no retry of the tuning. Extra row runs: one, for filebrowser (a CI infrastructure failure, see Results). The follow-up run 45439008280 (see Follow-up) is the one further run: 23 rows, once.

## Results

Run 45432518617 (workflow top30, branch check/top30, commit a3d780f). Each job used: dock2nano a3d780f (feat/barenames 5677ee9 merged, which contains feat/modules 35e8082), ops 2177c549df3fab2662d6386042c74bec859275bd (the pin of compose-up.yml). Run 45431721230 (commit 8de0e48, base feat/modules without barenames) was cancelled as superseded and is not a result.

Counts of 30: converted 18 (convert exit 0, compose.yaml written); started 11 (ops compose up ok); answered 6 (the probe got an HTTP status or an open TCP port); green 5 (2xx/3xx or open port).

Extra row run: filebrowser, job 138581409098 failed in the step that builds dock2nano, with "##[error] ... github.com/go-ini/ini@v1.52.0: read https://proxy.golang.org/... stream error: stream ID 525; INTERNAL_ERROR". Rerun of that job alone (job 138596712813, same run): the row ran and the docker build failed. The rerun is the only extra run.

Convert was not run for the 12 rows whose docker build failed (my choice, not in the instructions): what dock2nano reports for those Dockerfiles is not measured in this run. The follow-up below measures it.

| row | repository | converted | started | probe | run id (job) | first error / reason |
| --- | --- | --- | --- | --- | --- | --- |
| whoami | traefik/whoami | yes | yes | success, 200 on 80 | 45432518617 (138581408988) | none |
| httpbin | postmanlabs/httpbin | yes | yes | no answer (000 x24) | 45432518617 (138581408983) | console: "OSError: [Errno 38] Function not implemented" at os.fork in gunicorn spawn_worker |
| echo-server | jmalloc/echo-server | no | no | not run | 45432518617 (138581408924) | docker build: "/artifacts/build/release/linux/amd64/echo-server": not found |
| datasette | simonw/datasette | no | no | not run | 45432518617 (138581408894) | docker build: "HTTP error 404 while getting https://github.com/simonw/datasette/archive/refs/tags/.zip" (VERSION empty) |
| kanboard | kanboard/kanboard | yes | no | not run | 45432518617 (138581408379) | ops compose up 3 of 3: "ip timeout" |
| ttyd | tsl0922/ttyd | no | no | not run | 45432518617 (138581408878) | docker build: "/dist/amd64/ttyd": not found |
| gitea | go-gitea/gitea | yes | no | not run | 45432518617 (138581409132) | ops compose up 3 of 3: "ip timeout" |
| gogs | gogs/gogs | yes | no | not run | 45432518617 (138581408958) | ops compose up 3 of 3: "ip timeout" |
| filebrowser | filebrowser/filebrowser | no | no | not run | 45432518617 (rerun 138596712813; first try 138581409098 infra) | docker build: "/filebrowser": not found |
| changedetection | dgtlmoon/changedetection.io | yes | yes | success, 200 on 5000 | 45432518617 (138581408801) | none |
| dashy | lissy93/dashy | yes | yes | no answer (000 x24) | 45432518617 (138581408865) | after the wait ops instance list shows only dns; console has no program line |
| homepage | gethomepage/homepage | yes | yes | reached, 400 x24 | 45432518617 (138581408805) | body: {"error":"Host validation failed. See logs for more details."} (the probe sends Host ip:port) |
| verdaccio | verdaccio/verdaccio | yes | yes | no answer (000 x24) | 45432518617 (138581408914) | after the wait ops instance list shows only dns; console has no program line |
| http-echo | mendhak/docker-http-https-echo | yes | yes | success, 200 on 8080 | 45432518617 (138581409210) | none |
| shlink | shlinkio/shlink | no | no | not run | 45432518617 (138581409134) | docker build: "Failed to download rr", "1 of 1 download(s) failed" |
| directus | directus/directus | yes | yes | no answer (000 x24) | 45432518617 (138581409306) | after the wait ops instance list shows only dns; console has no program line |
| ntfy | binwiederhier/ntfy | no | no | not run | 45432518617 (138581408916) | docker build: "/ntfy": not found |
| mailpit | axllent/mailpit | yes | yes | success, 200 on 8025 | 45432518617 (138581408755) | none (EXPOSE lists 1025 first; probe is 8025) |
| coredns | coredns/coredns | no | no | not run | 45432518617 (138581408912) | docker build: "/coredns": not found |
| prometheus | prometheus/prometheus | no | no | not run | 45432518617 (138581409499) | docker build: "/.build/linux-amd64/prometheus": not found |
| etcd | etcd-io/etcd | no | no | not run | 45432518617 (138581409055) | docker build: "/etcd--linux-amd64/etcd": not found |
| syncthing | syncthing/syncthing | yes | no | not run | 45432518617 (138581408927) | ops compose up 3 of 3: "ip timeout" |
| wallos | ellite/Wallos | yes | yes | no answer (000 x24) | 45432518617 (138581408904) | after the wait ops instance list shows only dns; console has no program line |
| excalidraw | excalidraw/excalidraw | yes | yes | success, 200 on 80 | 45432518617 (138581408774) | none |
| tile38 | tidwall/tile38 | no | no | not run | 45432518617 (138581408715) | docker build: "/packages/tile38--linux-amd64/tile38-benchmark": not found |
| redlib | redlib-org/redlib | no | no | not run | 45432518617 (138581409145) | docker build: RUN curl of the release tarball "did not complete successfully: exit code: 1" |
| homarr | ajnart/homarr | no | no | not run | 45432518617 (138581408680) | docker build: "/.next/static": not found |
| glance | glanceapp/glance | yes | no | not run | 45432518617 (138581409083) | ops compose up 3 of 3: "ip timeout" |
| linkwarden | linkwarden/linkwarden | yes | no | not run | 45432518617 (138581408940) | ops compose up 3 of 3: "ip timeout" (the report also prints a Prisma error: container stopped with status 1) |
| reactive-resume | reactive-resume/reactive-resume | yes | no | not run | 45432518617 (138581408967) | ops compose up 3 of 3: "ip timeout" |

Not converted, quoted from the report (converted rows only): homepage: "unsupported dockerfile: not converted: line 45: COPY --link". Healthcheck notes (not run by ops compose): dashy, excalidraw, homepage, kanboard, wallos. Process notes ("unsupported processes ... started N child processes ... nanos has no fork"): httpbin, verdaccio, dashy, gitea. Other notes printed: "warning: port N could not be asked for /: dial tcp ...: connect: connection refused" (most converted rows), "could not read /proc/net/tcp ... exit status 126/128" (whoami, syncthing, glance), "approximated user" (http-echo), "note expose: expose and the EXPOSE of the image ... do not publish a port" (changedetection, http-echo).

Post-up: no report printed a "then, once X is up" line in any of the 18 converted rows, so no post-up command ran.

Address of each started row: from the console "assigned 192.168.33.N" line (ops instance logs).

## Follow-up: run 45439008280

Run 45439008280 (workflow top30, branch check/top30, matrix commit 82c0ea4). The matrix is 23 rows: the 12 rows of the first run whose docker build failed, the 7 with "ip timeout", and the 4 with no answer (dashy, verdaccio, directus, wallos). The 7 that answered in the first run were not rerun. Ops is the same pin, 2177c549. The tool code is the same as a3d780f: `git diff a3d780f HEAD -- '*.go'` is empty since then; the ROW lines print dock2nano=82c0ea4 because that commit is the checkout of the job (it holds the workflow change only). Commit dff626d (skip ci, not run) fixes one line of the workflow, see below.

Conclusion of the run: failure, as in the first run (a red row is a row that did not answer). Jobs: 22 ran their row to the end. Linkwarden failed in the step "ops from source", before its row ran: `go build -o /tmp/ops .` printed "provider/aws/aws.go:12:2: github.com/aws/aws-sdk-go-v2/service/ebs@v1.27.10: read ... stream error: stream ID 117; INTERNAL_ERROR". This is the same kind of infrastructure failure as the filebrowser first try. Not rerun: the follow-up was meant to run once.

Two changes of the workflow in this follow-up, both in .github/workflows/top30.yml: (1) when the docker build fails, the row runs convert on the Dockerfile as it is (same flags as the converted rows) and stops; (2) on every failed try of ops compose up, and at the end of each row, the workflow saves "ops instance list" and "ops instance logs NAME" (files instances-attempt-N.txt, console-attempt-N.txt, instances-final.txt, console-final.txt in the artifact). Defect found in the run: under the default shell of GitHub Actions (bash -e), a failing convert ended the step before its ROW lines, so the lines "ROW convert after the failed docker build" did not print for any row. The tool output is in the artifact (report-after-build-fail.txt) and the exit code is the step exit (1), read from the step log. Commit dff626d makes the line `|| xexit=$?`; it was not run.

### A. Docker build failed (11 rows; shlink built this time)

Shlink: its docker build succeeded in this run (117 seconds). In the first run it failed with "Failed to download rr". It converted (convert_exit=0) and is in table C.

For the other 11, the tool printed (report-after-build-fail.txt, 9 to 10 lines each): the first line "NAME: building dock2nano/NAME/NAME:latest", then "dock2nano: service NAME: docker build: docker build --file /tmp/w/NAME/Dockerfile --tag ... --quiet -- /tmp/w/NAME: exit status 1: Dockerfile:N", then the Dockerfile excerpt with the failing line marked ">>>", then the docker ERROR line. No dock2nano line names the missing file or says what to do; the report says nothing else. No compose directory was written. The step exit code is 1 for all 11.

| row | line 2 of the tool output (quoted, cut) | docker ERROR line (the cause, as docker printed it) | first run said |
| --- | --- | --- | --- |
| echo-server | "dock2nano: service echo-server: docker build: ... exit status 1: Dockerfile:3" | "/artifacts/build/release/linux/amd64/echo-server": not found | same |
| datasette | "... service datasette: ... exit status 1: Dockerfile:13" | process "/bin/sh -c pip install https://github.com/simonw/datasette/archive/refs/tags/${VERSION}.zip ..." did not complete successfully | HTTP 404 on the same URL |
| ttyd | "... service ttyd: ... exit status 1: Dockerfile:9" | "/dist/amd64/ttyd": not found | same |
| filebrowser | "... service filebrowser: ... exit status 1: Dockerfile:21" | "/filebrowser": not found | same |
| ntfy | "... service ntfy: ... exit status 1: Dockerfile:16" | "/ntfy": not found | same |
| coredns | "... service coredns: ... exit status 1: Dockerfile:8" | "/coredns": not found | same |
| prometheus | "... service prometheus: ... exit status 1: Dockerfile:18" | "/.build/linux-amd64/promtool": not found | "/.build/linux-amd64/prometheus" (docker names the first missing COPY; it varies) |
| etcd | "... service etcd: ... exit status 1: Dockerfile:9" | "/etcd--linux-amd64/etcdutl": not found | "/etcd--linux-amd64/etcd" (same reason) |
| tile38 | "... service tile38: ... exit status 1: Dockerfile:11" | "/packages/tile38--linux-amd64/tile38-benchmark": not found | same |
| redlib | "... service redlib: ... exit status 1: Dockerfile:7" | process "/bin/sh -c curl -L ...redlib-${TARGET}.tar.gz ... | tar xz" did not complete successfully: exit code: 1 | same |
| homarr | "... service homarr: ... exit status 1: Dockerfile:18" | "/.next/static": not found | same |

### B. ip timeout (6 rows run, linkwarden not run in this follow-up)

Compared with whoami (first run, job 138581408988, artifact top30-whoami): its up.log has "spawning instance.." and no "couldn't find mac" line; its console has "[3.348349] en1: assigned 192.168.33.85" and "Starting up on port 80"; ops instance list shows whoami Running at 192.168.33.85. The six B rows below had, in all three tries, "couldn't find mac" and "mac is an empty string" (pairs), and no "en1: assigned" line in any console.

| row | docker build, convert | ops compose up tries | "couldn't find mac" lines per try (up.log) | console of the instance (instance logs, after try 3) | program output in the console | the report (observation, same as first run) |
| --- | --- | --- | --- | --- | --- | --- |
| kanboard | ok, 0 | 3 of 3 "ip timeout" | 19 | 0 lines | none | "the program started 9 child processes ... nanos has no fork" |
| gitea | ok, 0 | 3 of 3 "ip timeout" | 19 | 0 lines | none | "the program started 36 child processes" |
| gogs | ok, 0 | 3 of 3 "ip timeout" | 19 | 0 lines | none | "the program started 200 child processes" |
| syncthing | ok, 0 | 3 of 3 "ip timeout" | 18 | 2 lines | yes: "2026-11-24 16:41:28 INF Invalid version string "992ae894";" then "does not match regexp ^v\d+\.\d+\.\d+..." | "the container stopped with status 1 before the end of the observation" |
| glance | ok, 0 | 3 of 3 "ip timeout" | 18 | 1 line | yes: "parsing config: reading /app/config/glance.yml: open /app/config/glance.yml: no such file or directory" | "the container stopped with status 1" |
| reactive-resume | ok, 0 | 3 of 3 "ip timeout" | 17 | 20 lines | yes: "Error [ERR_MODULE_NOT_FOUND]: Cannot find module '/app/apps/server/node_modules/@t3-oss/env-core/dist/standard.js' imported from /app/apps/server/node_modules/@t3-oss/env-core/dist/index.js" | "the container stopped with status 1" |
| linkwarden | not run in this follow-up (ops build failed, see above) | first run: 3 of 3 "ip timeout", 19 lines per try | first run: 19 | first run: not captured | first run report: "Prisma schema loaded from packages/prisma/schema.prisma | Error: ..." | first run: "the container stopped with status 1" |

Every try of the six also printed "no sysroot in package", "spawning instance..", and in try 1 "ip link show dev ops0 exit status 1" (as whoami did). In each of the six, the instance list at the end of try 3 showed only the dns instance (the program instance is absent or stopped). Guest booted: the console of kanboard, gitea and gogs is empty, so nothing can be said about the boot; syncthing, glance and reactive-resume printed the program's own error and no "en1" line (no kernel or network line at all), so the console shows no address for the guest. What the output shows is that the program exited with status 1 (glance, reactive-resume, syncthing) or that nothing was printed (kanboard, gitea, gogs). Not established: why ops got no mac for the instance.

### C. No answer (000 x24): dashy, verdaccio, directus, wallos, shlink

| row | ops compose up | address (console "assigned") | probe codes | console at the end (instance logs) | instance list at the end | program output |
| --- | --- | --- | --- | --- | --- | --- |
| dashy | ok, try 1 | 192.168.33.90 | 000 x24 | 2 lines: "[3.346108] en1: assigned 192.168.33.90" | dns and dashy, both Running | none |
| verdaccio | ok, try 1 | 192.168.33.162 | 000 x24 | 2 lines: "[3.476718] en1: assigned 192.168.33.162" | verdaccio Running | none |
| directus | ok, try 1 | 192.168.33.178 | 000 x24 | 2 lines: "[3.722702] en1: assigned 192.168.33.178" | directus Running | none |
| wallos | ok, try 1 | 192.168.33.238 | 000 x24 | 2 lines: "[3.443617] en1: assigned 192.168.33.238" | wallos Running | none |
| shlink | ok, try 1 | 192.168.33.48 | 000 x24 | 2 lines: "[3.396552] en1: assigned 192.168.33.48" | shlink Running | none |

The program instance is Running in ops and has an address, the guest got its address, and the program printed nothing in the console. Correction of the first run: its text said "after the wait ops instance list shows only dns". That is wrong: the first run's instances.txt of dashy, verdaccio, directus and wallos lists the program instance as Running (for example dashy 192.168.33.150). Not diagnosed: whether the program listens on the port of the probe; no HTTP answer on any try.

## Observed failure causes, grouped

Fork or CI: filebrowser first try, job 138581409098: "stream error: stream ID 525; INTERNAL_ERROR" from proxy.golang.org while building dock2nano (infrastructure, rerun allowed).

Missing file in the build context (prebuilt binary or build output not in the repository): ntfy "/ntfy": not found; coredns "/coredns": not found; prometheus "/.build/linux-amd64/prometheus": not found; etcd "/etcd--linux-amd64/etcd": not found; ttyd "/dist/amd64/ttyd": not found; echo-server "/artifacts/build/release/linux/amd64/echo-server": not found; tile38 "/packages/tile38--linux-amd64/tile38-benchmark": not found; homarr "/.next/static": not found; filebrowser "/filebrowser": not found.

Network or build argument: datasette "HTTP error 404 while getting https://github.com/simonw/datasette/archive/refs/tags/.zip" (VERSION is empty, no value given); shlink "Failed to download rr"; redlib curl of the release tarball, exit code 1.

Program: httpbin "OSError: [Errno 38] Function not implemented" (os.fork, gunicorn workers; nanos has no fork, as docs/limits.md "No fork" says).

Ip timeout, ops compose up failed 3 of 3: kanboard, gitea, gogs, syncthing, glance, linkwarden, reactive-resume ("ip timeout" in up.log, every try; the follow-up, table B, has the console and the program output).

No answer, the instance is Running and silent: verdaccio, wallos, directus, dashy, shlink (see the follow-up, table C), and httpbin (see above, the fork error). Not diagnosed. (The first run's text said the instance list showed only dns: that was wrong, see the follow-up.)

Port or Host: homepage 400 "Host validation failed" (the probe sends the address as Host; the same effect is described in docs/coverage.md for django).

## Documentation check

Every item: file and line, what was unclear, missing, wrong, or contradicted by the tool output.

1. README.md, named in the brief as the first document to read: there is no README.md at the root of the repository (checked at check/top30). Nothing else in the docs describes the usage of convert as a whole. Guess: the usage text of the tool (cmd/app.go, lines 36 to 122) was used as the instruction.
2. --dockerfile mode: not in docs/ at all (grep of docs/*.md finds no --dockerfile, no --name, no --context). The only description is the usage text, cmd/app.go lines 49 to 53. What I had to guess: that the context default is the Dockerfile directory and that the service name is the directory name (cmd/app.go line 53 says so; I relied on it).
3. CLAUDE.md line 12: "Si chiama ops (ops pkg from-docker --observe, ops compose) per cio che ops gia fa". Contradicted: the tool never runs the ops binary (it prints the commands; there is no exec of ops in internal/ or cmd/). The observation runs through docker with the code in internal/observe.
4. docs/limits.md line 122: the observation is described as "from-docker --observe, a port of nanovms/ops#1802". Contradicted by the code (see 3). Also, the ops fork branches named for it (feat/from-docker-trace, feat/from-dockerfile in AngeloRubens/ops) are not on top of the 2177c549 pin (diverged), so a reader cannot reproduce the observation with the pin.
5. docs/limits.md line 142: "ops compose up panics inside ops when the dns instance is late; the tool needs retries". The tool prints one ops compose up command and no retry; a user who runs the printed command gets no retry. What I had to guess: the number of tries (I used three, as compose-up.yml does).
6. Printed next steps (cmd/app.go nextSteps, about line 466 onward): "sudo env OPS_HOME=... PATH="$PATH" ops compose up -f ..." is printed with no note on the ip timeout that all 7 failed rows showed. Missing.
7. Report notes that no doc explains (grep of docs/ finds only docs/results.md line 39 for "could not be asked"): "warning: port N could not be asked for /: dial tcp ...: connection refused"; "note expose: expose and the EXPOSE of the image ... do not publish a port"; "approximated user"; "unsupported processes: ... nanos has no fork" (docs/limits.md has a No fork section, but the printed text does not point to it); "could not read /proc/net/tcp ... exit status 126". Missing.
8. Dockerfile instructions the tool does not convert: "unsupported dockerfile: not converted: line 45: COPY --link" (homepage) is printed, but no doc lists the Dockerfile instructions that are not converted (docs/limits.md has no Dockerfile section). Missing.
9. --seconds: default 30 (cmd/app.go line 58); no doc says how long a Dockerfile service needs to be observed. compose-up.yml uses 90 in CI, which is not written in the docs. I used the default.
10. The docker build of a Dockerfile that copies a file not in the repository (12 rows): the tool's own error is not known to me because I did not run convert for them (see Results). The docs say nothing about a missing build input for a Dockerfile.
11. EXPOSE with a variable (homepage "EXPOSE $PORT", verdaccio "EXPOSE $VERDACCIO_PORT", http-echo "EXPOSE $HTTP_PORT $HTTPS_PORT"): no doc says which port the probe should use; I used the value of the ENV or ARG of the Dockerfile.
12. Post-up: no row printed a post-up line, so the step in the printed next steps was not exercised; the docs and the printed text could not be checked on a real run here.

## What is not measured

Conversion of the rows with a docker build failure: measured in the follow-up (11 rows; the output of convert for the Dockerfile as it is). Linkwarden in the follow-up (its job failed in the ops build). Post-up scripts. Any program that answered 200 beyond the first try. The cause of the no-answer rows (verdaccio, wallos, directus, dashy, httpbin except the fork error). The ip timeout causes (only the first line of up.log is quoted).

## Rerun on feat/modules d784c7f: runs 45462110286 and 45467614282

Two runs of the workflow top30 on the branch check/top30-d784 (feat/modules d784c7f, which contains the build-failure hint and the bare-name notes), ops 2177c549, the 23 rows of the follow-up. Run 45462110286: 17 rows ran to their ROW result; 6 (dashy, datasette, echo-server, glance, reactive-resume, syncthing) failed before it, in the step that builds ops from source (go build, module download from proxy.golang.org); echo-server also printed a source=fail line from the checkout of its sha in the same step. Run 45467614282: those 6 rows only, with the go build of ops and of the tool tried up to 3 times (dock2nano 782a5e0, the same tool code plus the workflow change); all 6 ran to their ROW result. Both runs concluded with failure, as before (a red row is a row that did not answer).

Nothing answered in the rerun. Per row:

| row | result in the rerun | same as the follow-up |
| --- | --- | --- |
| coredns, etcd, filebrowser, homarr, ntfy, prometheus, redlib, tile38, ttyd | docker build failed, convert run on the Dockerfile as it is, exit 1, no compose.yaml | yes (same docker errors) |
| echo-server | docker build failed ("/artifacts/build/release/linux/amd64/echo-server": not found), convert exit 1 | yes |
| datasette | docker build failed (pip install of the zip of ${VERSION} did not complete successfully), convert exit 1 | yes |
| gitea, gogs, kanboard, linkwarden | converted, ops compose up "ip timeout" in 3 of 3 tries; console of the instance: 0 lines (gitea, gogs, kanboard), 1 line (linkwarden) | yes; linkwarden now measured (not run in the follow-up) |
| syncthing, glance, reactive-resume | converted, "ip timeout"; console 2, 1 and 20 lines | yes |
| dashy | converted, started, address 192.168.33.16:8080, 24 tries all 000 | yes |
| directus, shlink, verdaccio, wallos | converted, started, 24 tries all 000 | yes |

The hint of the tool on a failed docker build was printed after the docker text in every row whose build failed: 11 of 11 rows (the 9 of the first run plus echo-server and datasette in the second): "the Dockerfile copies \"<path>\" and it is not in the build context ..." for the missing files (the path as docker named it, for example /.build/linux-amd64/promtool, /etcd--linux-amd64/etcdutl), and "a RUN step of the Dockerfile fails, so the build itself fails: run docker build without --quiet ..." for redlib and datasette. The hint is a guess about the cause, not a measurement of it.

Not measured: the cause of the rows with 000 (dashy, directus, shlink, verdaccio, wallos) and of the ip timeout of gitea, gogs, kanboard and linkwarden (their consoles have 0 or 1 lines).
