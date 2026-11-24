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

Rules: no Dockerfile edited, no environment given, no retry of the tuning. Extra row runs: one, for filebrowser (a CI infrastructure failure, see Results).

## Results

Run 45432518617 (workflow top30, branch check/top30, commit a3d780f). Each job used: dock2nano a3d780f (feat/barenames 5677ee9 merged, which contains feat/modules 35e8082), ops 2177c549df3fab2662d6386042c74bec859275bd (the pin of compose-up.yml). Run 45431721230 (commit 8de0e48, base feat/modules without barenames) was cancelled as superseded and is not a result.

Counts of 30: converted 18 (convert exit 0, compose.yaml written); started 11 (ops compose up ok); answered 6 (the probe got an HTTP status or an open TCP port); green 5 (2xx/3xx or open port).

Extra row run: filebrowser, job 138581409098 failed in the step that builds dock2nano, with "##[error] ... github.com/go-ini/ini@v1.52.0: read https://proxy.golang.org/... stream error: stream ID 525; INTERNAL_ERROR". Rerun of that job alone (job 138596712813, same run): the row ran and the docker build failed. The rerun is the only extra run.

Convert was not run for the 12 rows whose docker build failed (my choice, not in the instructions): what dock2nano reports for those Dockerfiles is not measured.

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

## Observed failure causes, grouped

Fork or CI: filebrowser first try, job 138581409098: "stream error: stream ID 525; INTERNAL_ERROR" from proxy.golang.org while building dock2nano (infrastructure, rerun allowed).

Missing file in the build context (prebuilt binary or build output not in the repository): ntfy "/ntfy": not found; coredns "/coredns": not found; prometheus "/.build/linux-amd64/prometheus": not found; etcd "/etcd--linux-amd64/etcd": not found; ttyd "/dist/amd64/ttyd": not found; echo-server "/artifacts/build/release/linux/amd64/echo-server": not found; tile38 "/packages/tile38--linux-amd64/tile38-benchmark": not found; homarr "/.next/static": not found; filebrowser "/filebrowser": not found.

Network or build argument: datasette "HTTP error 404 while getting https://github.com/simonw/datasette/archive/refs/tags/.zip" (VERSION is empty, no value given); shlink "Failed to download rr"; redlib curl of the release tarball, exit code 1.

Program: httpbin "OSError: [Errno 38] Function not implemented" (os.fork, gunicorn workers; nanos has no fork, as docs/limits.md "No fork" says).

Ip timeout, ops compose up failed 3 of 3: kanboard, gitea, gogs, syncthing, glance, linkwarden, reactive-resume ("ip timeout" in up.log, every try).

No answer, instance gone or silent: verdaccio, wallos, directus, dashy, httpbin (see above). Not diagnosed: the instance list after the wait shows only dns, and the console has no program line.

Port or Host: homepage 400 "Host validation failed" (the probe sends the address as Host; the same effect is described in docs/coverage.md for django).

## Documentation check

Every item: file and line, what was unclear, missing, wrong, or contradicted by the tool output.

1. README.md, named in the brief as the first document to read: there is no README.md at the root of the repository (checked at check/top30). Nothing else in the docs describes the usage of convert as a whole. Guess: the usage text of the tool (cmd/app.go, lines 37 to 76) was used as the instruction.
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

Conversion of the 12 rows with a docker build failure. Post-up scripts. Any program that answered 200 beyond the first try. The cause of the no-answer rows (verdaccio, wallos, directus, dashy, httpbin except the fork error). The ip timeout causes (only the first line of up.log is quoted).
