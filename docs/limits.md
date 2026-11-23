# Limits

The goal of dock2nano is to reuse every existing Dockerfile and docker-compose.yml unchanged, for all scenarios. It aims at that and does not guarantee it. What the tool cannot convert is printed in its report, never dropped silently. This file lists what does not convert or does not run on nanos, why, and the evidence.

Each entry says whether it is a hard limit (it comes from nanos, ops or the nature of a unikernel and the tool cannot remove it) or a gap (the tool could close it, or it is open work). Run ids are GitHub Actions runs on the fork of the project, unless another repository is named. A result is stated only if it was run.

## Summary

| Entry | Kind | Evidence |
| --- | --- | --- |
| No fork in nanos | hard limit | run 44808943029 |
| Programs run as root | hard limit | observed, no run id recorded |
| FUTEX_REQUEUE missing in nanos | open nanos work | nanovms/nanos work on a fork, no run id recorded |
| nginx resolves its upstream at start | gap, worked around | nanovms/ops#1813, runs 44583469624, 44612626939, 44612633466 |
| ops-dns answers only .service names | hard limit of ops-dns, worked around | nanovms/ops#1812, nanovms/ops#1815 |
| Volumes | hard limit | printed by the tool |
| Networks and network_mode | hard limit | printed by the tool |
| Replicas | gap | printed by the tool |
| Healthcheck | hard limit of ops compose | printed by the tool |
| gunicorn, npm, nodemon fork or spawn processes | hard limit, partly a gap | run 44808943029, no run id recorded for npm and nodemon |
| Code paths never run during the observation | hard limit of the method | tzdb.dat case |
| JVM crashes with compiled code | open | petclinic, no run id recorded |
| ops compose reports ip timeout although the guest has an address | gap in ops | runs recorded in the project notes only as a matrix |
| Hub postgres package has no entrypoint | gap | see manual-changes.md |

## No fork

Cause: nanos is a unikernel with one process and no fork. A program that calls fork fails.

Kind: hard limit.

Evidence: run 44808943029 (2026-11-22), jobs fastapi, fastapi2, fastapi3. The shebang fix worked and the guest started the right program (gunicorn 23.0.0 under uvicorn workers, "Listening at 0.0.0.0:8000"), then died with os.fork() OSError 38. The api answered 000 (no answer).

What the tool does: nothing yet. A way to override the service command so that a single-process variant is used is the next feature (see manual-changes.md).

## Programs run as root

Cause: nanos runs the program as root. A program that refuses root does not start. mysqld is the case seen.

Kind: hard limit of nanos. The program can be told to accept root with its own option (mysqld --user=root).

Evidence: found while converting petclinic (project notes, 2026-11-08). No run id recorded.

What the tool does: nothing automatic. The option is a manual change (see manual-changes.md).

## FUTEX_REQUEUE missing in nanos

Cause: nanos does not implement FUTEX_REQUEUE (op 3). musl pthread_cond_broadcast uses it (the Alpine node:14 image), so the node web1 service of nginx-nodejs-redis does not start.

Kind: gap in nanos, not in the tool. Work is on a fork of nanos (branch futex-requeue-final, four commits on top of master e4b9c9e6). It is not opened on nanovms/nanos.

Evidence (2026-11-20): with a kernel that implements it, 4 of 4 jobs start; with the nightly kernel, 0 of 4 web1 start (earlier revision, kernel built from master of 2026-11-08). In the full compose-up matrix on ops master 2177c54 nginx-nodejs-redis is red (8 of 10 green). No run id recorded.

Side finding: the mmap runtime test of nanos hangs after "thp test: OK" on plain origin/master too (0 of 30 with 1 vcpu, 7 of 30 with 2, 6 of 30 with 4). It is not caused by the futex work.

## nginx resolves its upstream at start

Cause: nginx resolves the upstream name at start, before the guest has an address. It exits with "host not found in upstream backend.service". The hub nginx dies too; a literal IP works.

Kind: gap of ops compose, reported as nanovms/ops#1813. KVM does not fix it: local run of nginx-golang with KVM forced, check/nginx-pack run 43510838986 (2026-11-14), 3 of 3 runs failed with the same message, with dns and backend addressed at about 3.2 s.

Workaround: the ops maintainer suggested the manifest option exec_wait_for_ip4_secs (nanos waits for an ipv4 address via DHCP; the key has ip4 in it). Measured on nginx-golang: with the option "30" compose up was ok at attempt 1 and proxy and backend both answered 200; without it compose up failed 3 of 3 (run 44583469624). Runs 44612626939 and 44612633466 repeat it: 3 of 3 with and 3 of 3 without. The tool writes the option ("30") for every service with depends_on on a converted service, with a note "address wait". It was measured on nginx only.

Matrix of 2026-11-20 (ops master 2177c54, with the address wait): 8 of 10 green, red were nginx-nodejs-redis (FUTEX_REQUEUE, see above) and react-express-mongodb. Before the address wait, nginx jobs that were green had passed only without a real front end.

## ops-dns answers only .service names

Cause: ops-dns (version 0.0.1, source not public) answers only names under .service. A bare record is accepted by /add (200) but queried with SERVFAIL. A service named db is db.service on ops. So dns_names plain or both cannot work.

Kind: hard limit of ops-dns, worked around. Reported as nanovms/ops#1812 (short names, dns_search). The fix is nanovms/ops#1815 (dns_search, merged 2026-11-18), which writes "search service" in the resolv.conf of each guest.

Tool flag: --short-names writes dns_search: [service] in compose.yaml. Verified on nanos: the sparkjava backend resolved db (the mariadb log shows its connection).

## Volumes

Cause: nanos has no volumes in the Docker sense. The tool prints, per volume, one of: named volume (data not shared or kept), anonymous volume (data not kept), a source with a variable (not known whether bind or named, not converted), an unsupported volume type. The VOLUME paths of an image are noted as not kept.

Kind: hard limit for named volumes and sharing. Bind mounts are converted by copying files (see the bind code in cmd). File secrets are placed at /run/secrets.

Evidence: printed by the tool (internal/gen/gen.go). Data retention of postgres across boots is not measured.

Also: each package gets a base_volume_sz of the files plus 25 percent plus 256 MB (mysqld ran out of space before this). What dependents write into a dependency is not in the package of the dependency; the report warns about it.

## Networks and network_mode

Cause: every package is on the bridge ops0. Extra networks and network_mode are printed as not converted. Docker FORWARD DROP also blocks guest-to-guest traffic on ops0 on a machine with Docker (seen on a local run).

Kind: hard limit of ops compose.

## Replicas

Cause: one instance is generated. replicas above 1 is printed ("one instance is generated"); replicas 0 means the service is not converted.

Kind: gap. The tool could emit several packages, it does not.

## Healthcheck

Cause: ops compose runs no healthchecks. A healthcheck in the compose file or a HEALTHCHECK in the Dockerfile is printed with its first word and the number of words (the rest may hold a password). The options interval, timeout, retries, start_period and start_interval are named as not converted. The tool itself waits for every exposed port with a 10 s grace in its own checks.

Kind: hard limit of ops compose.

## gunicorn, npm, nodemon

Cause: these start child processes. gunicorn forks workers (no fork, see above). npm start forks, and lazily loaded modules are not carried because the observation did not reach them. nodemon (react-express-mongodb) is red in the compose-up matrix.

Kind: hard limit for the fork itself; a gap for the tool, which could run the real program directly instead of the wrapper.

Evidence: gunicorn run 44808943029. npm: project notes 2026-11-10 (npm start images break). nodemon: react-express-mongodb red in the matrix of 2026-11-20. No run ids recorded for the last two. Single-process variants are needed for fastapi (uvicorn) and django (runserver --noreload); the option --command gives them. The report now suggests the --command for three cases, from the observed command line: django runserver without --noreload, uvicorn with --workers above 1 or --reload, gunicorn with the worker class of uvicorn (a command for uvicorn; for any other worker class of gunicorn only a note that a single-process server has to be chosen). The suggestion is a guess to review; it is not applied. It repeats only the options of an allowlist (an option outside it, which may hold a secret, gives no command), writes --workers 1 for uvicorn, and says when the service starts the program through another one. Measured in the fork run 45223574856: the fastapi suggestion (gunicorn to uvicorn, with host and port said to be assumed), the django suggestion (--noreload), and django with --command alone, which printed the warning that the Host was rejected and names --exercise-host; the ci run 45223366657 and the integration run 45223366390 were green. Not measured: uvicorn with --workers or --reload, the body of a 400 of Django with DEBUG off, webpack, Rails, Vite, and WEB_CONCURRENCY in the environment. A request of --exercise that the program answers with a status of 400 or more is a warning; when the status is 400 or 403 and the start of the body is a Host rejection of Django, webpack or Vite, or Rails, the warning names --exercise-host SVC=localhost.

## Code never run during the observation

Cause: the observation (from-docker --observe, a port of nanovms/ops#1802) runs the image under strace and carries the files it saw. What does not run during the observation is not seen. Known limits of the observation: it needs Docker, it takes strace from an Alpine image with the musl loader, and the choice of the program is an estimate.

Kind: hard limit of the method.

Evidence: sparkjava backend failed on a missing tzdb.dat after db was resolved (the JVM file was not carried because the observation never reached it); fastapi after a changed Args failed with "No module named runpy" (run 45003079974, see manual-changes.md); the mongo and node lazy modules. The output is what was observed and has to be reviewed; the tool says so.

## JVM crashes with compiled code

Cause: on nanos, JIT-compiled code crashed the JVM at random places in the petclinic (GlassFish) case: 5 of 5 runs without -Xint failed. Java servers under emulation were also not stable: 12 Java servers started on CI but not stably (ActiveMQ, Helidon, WildFly sometimes crash).

Kind: open, cause not isolated. Workaround is -Xint, manual.

Evidence: petclinic-up workflow (2026-11-08); no run id recorded.

## ip timeout from ops

Cause: ops often reports "ip timeout" although the guest has an address. Two causes were seen. The arp lookup missed a guest that had already printed its address (suspected cause stated in nanovms/ops#1803, not verified; the arp fix is nanovms/ops#1805, merged). On an Italian locale the ops regex needs the English "at" in the output of arp -a; with LC_ALL=C it works. The second one is not isolated from a temporary iptables accept on ops0 and is not reported to ops.

Kind: gap in ops.

Also: ops compose up panics inside ops when the dns instance is late; the tool needs retries.

## Hub packages

The hub package for postgres has no entrypoint: POSTGRES_DB, POSTGRES_USER, POSTGRES_PASSWORD and initdb.d are ignored, and the tool does not create the database; it prints the psql CREATE DATABASE command. A second boot after a kill fails on a stale postmaster.pid. Data retention is not measured. The branch for POSTGRES_USER different from the superuser is unit-tested only.
