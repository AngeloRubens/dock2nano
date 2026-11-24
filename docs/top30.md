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

Rules: no Dockerfile edited, no environment given, no retry of the tuning. Extra row runs: none so far (stated in the results).
