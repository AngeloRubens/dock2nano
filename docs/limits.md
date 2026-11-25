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
| Init wrappers (tini, dumb-init) chosen as the program | gap, fixed | before: dashy run 45467614282 and verdaccio run 45462110286 (000 x24); after: run 45489113069 (200 on both) |
| Process supervisors (s6-svscan, runsvdir, supervisord) | hard limit, one recipe measured | run 45489113069: gitea answers 200 with command gitea web; kanboard and wallos with php -S did not answer |
| Programs that need a config file, variables or another service | not a limit of nanos, printed by the tool | run 45485671510 (docker run of the image alone fails the same way), run 45489113069 (glance 200 with its config) |
| Files opened through a linked directory (pnpm node_modules) | gap, fixed | reactive-resume, console of run 45467614282 before, run 45489113069 after |
| The home of the user of the image in the observation | gap, fixed | directus, run 45485671510 (EACCES under /root/.pm2) |
| The version of the build comes from git tags | workflow only | syncthing, run 45485671510 (200 with a full clone) |
| A program started by the process of the image through a thread | gap, fixed | shlink: the php worker taken as the program is in the reports of runs 45462110286 and 45489113069; after the fix, run 45492200295 |

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

Kind: hard limit of ops-dns, worked around. Reported as nanovms/ops#1812 (short names, dns_search). The fix is nanovms/ops#1815 (dns_search, merged 2026-11-18; ops master since 2177c54, not in any release up to 0.1.47), which writes "search service" in the resolv.conf of each guest.

Bare package names (db, backend) resolve inside the guests without any flag with an ops that includes nanovms/ops#1815 (ops master since 2177c54; not in any release up to 0.1.47). Measured: sparkjava-mysql and nginx-golang-mysql were green with no flags, run 45393646760. With a released ops (every release up to 0.1.47) a bare name does not resolve, with or without --short-names: that ops reads compose.yaml with a plain yaml.Unmarshal and ignores dns_search, so write it as <pkg>.service. Both statements about a released ops are read from the source of 0.1.47, not run; the <pkg>.service form was measured only on ops master and fork builds. --short-names writes dns_search: [service] in compose.yaml; only an ops that includes nanovms/ops#1815 reads it, and that ops already uses the search line by default, so the flag is redundant there. The line did work with an ops built from the #1815 branch (the earlier implementation), run 43266079611: the sparkjava backend resolved db, the mariadb log shows its connection. The compose service name resolves only when it equals the package name; this follows from the search domain and was not measured with a differing name.

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

Evidence: gunicorn run 44808943029. npm: project notes 2026-11-10 (npm start images break). nodemon: react-express-mongodb red in the matrix of 2026-11-20. No run ids recorded for the last two. Single-process variants are needed for fastapi (uvicorn) and django (runserver --noreload); the option --command gives them. The report now suggests the --command for three cases, from the observed command line: django runserver without --noreload, uvicorn with --workers above 1 or --reload, gunicorn with the worker class of uvicorn (a command for uvicorn; for any other worker class of gunicorn only a note that a single-process server has to be chosen). The suggestion is a guess to review; it is not applied. It repeats only the options of an allowlist (an option outside it, which may hold a secret, gives no command), writes --workers 1 for uvicorn (the last --workers given counts; the numeric options --timeout-keep-alive, --limit-concurrency, --backlog, --limit-max-requests, --timeout-graceful-shutdown and --h11-max-incomplete-event-size are carried), and says when the service starts the program through another one (a console script started through its shebang line is read as the command of the service). The note says that UVICORN_RELOAD is not covered by --workers 1, and for every gunicorn suggestion that the settings of the configuration file, gunicorn.conf.py and GUNICORN_CMD_ARGS are not carried, and that --chdir became --app-dir, which does not change the working directory. Measured in the fork run 45223574856: the fastapi suggestion (gunicorn to uvicorn, with host and port said to be assumed), the django suggestion (--noreload), and django with --command alone, which printed the warning that the Host was rejected and names --exercise-host; the ci run 45223366657 and the integration run 45223366390 were green. Not measured: uvicorn with --workers or --reload, the body of a 400 of Django with DEBUG off, webpack, Rails, Vite, and WEB_CONCURRENCY in the environment. A request of --exercise that the program answers with a status of 400 or more is a warning; when the status is 400 or 403 and the start of the body is a Host rejection of Django, webpack or Vite, or Rails, the warning names --exercise-host SVC=localhost. With --exercise-host given and a bare Bad Request (400) the warning says that the Host given may have been rejected.

## apache (apache2, httpd)

Cause: apache starts several processes (fork, prefork), and nanos has no fork. The option -DONE_PROCESS keeps one process.

Kind: hard limit for the fork itself; the report suggests a command for one image family, as a guess to review.

Evidence: apache-php (php:7.2-apache, ENTRYPOINT docker-php-entrypoint, CMD apache2-foreground) answers 200 on nanos with --command 'web=env APACHE_RUN_USER=www-data APACHE_RUN_GROUP=www-data APACHE_PID_FILE=/var/run/apache2/apache2.pid APACHE_RUN_DIR=/var/run/apache2 APACHE_LOCK_DIR=/var/lock/apache2 APACHE_LOG_DIR=/var/log/apache2 apache2 -DONE_PROCESS -DFOREGROUND' (run 45405287188). The same command without the env words fails with AH00111 (APACHE_RUN_DIR not defined), because apache2-foreground sources /etc/apache2/envvars (run 45402837918). apache2ctl instead of apache2 fails: it is a shell script (run 45407623718). nextcloud (the same apache family, with its own /entrypoint.sh that checks that the first word starts with apache) with the env form: apache starts and exits again and again, the cause was not found (run 45405287188). httpd -DONE_PROCESS on httpd:2.4-alpine got no answer on port 80 after 15 minutes (survey-run, runs 43418241488 and 43440436471), and the log of the guest ends with AH00557 (apr_sockaddr_info_get failed for 10-0-2-15) and AH00014 (Configuration check failed), probably the host name. That run is therefore not evidence against -DONE_PROCESS itself; it measured nothing about it.

What the tool does: when the observed program is apache2 with the single argument -DFOREGROUND (what apache2-foreground runs), the service starts it through apache2-foreground and the entrypoint is none or docker-php-entrypoint, the item "processes" prints the --command above, says that the user, group and directories are assumed, that it was measured on php:7.2-apache only and that nextcloud failed once for an unknown cause. The environment is a fixed list; the arguments of the user are never repeated (only their number is told, and with any other argument no command is given). The program must be apache2 or httpd itself (busybox httpd, started as an applet, is not read as apache). The script apache2-foreground must be a word of the command of the service on its own, not a piece of a shell script. After docker-php-entrypoint the item names apache2-foreground as the program started before apache2. For httpd no command is suggested, only because nothing was measured: the item says the one run made ended with AH00014. The unit tests of the report are in the ci run 45414476357 (the branch feat/apache-suggest at 1b6cf49); the earlier head was green in run 45412489832. Hypothesis, not run: the /entrypoint.sh of nextcloud installs and copies the files of the application only when "$1" starts with "apache"; with the env form $1 is "env", so that step is skipped and the package may lack the files of /var/www/html, which could be why apache exits again and again. It is not known to be the cause. Not measured: other Debian images that use apache2-foreground, other entrypoints, the httpd image with -DFOREGROUND added.

## Code never run during the observation

Cause: the observation (internal/observe; it does the work of from-docker --observe of nanovms/ops#1802 but is code of this tool, which runs docker and does not run ops) runs the image under strace and carries the files it saw. What does not run during the observation is not seen. Known limits of the observation: it needs Docker, it takes strace from an Alpine image with the musl loader, and the choice of the program is an estimate.

Kind: hard limit of the method.

Evidence: sparkjava backend failed on a missing tzdb.dat after db was resolved (the JVM file was not carried because the observation never reached it); fastapi after a changed Args failed with "No module named runpy" (run 45003079974, see manual-changes.md); the mongo and node lazy modules. The output is what was observed and has to be reviewed; the tool says so.

## JVM crashes with compiled code

Cause: on nanos, JIT-compiled code crashed the JVM at random places in the petclinic (GlassFish) case: 5 of 5 runs without -Xint failed. Java servers under emulation were also not stable: 12 Java servers started on CI but not stably (ActiveMQ, Helidon, WildFly sometimes crash).

Kind: open, cause not isolated. Workaround is -Xint, manual.

Evidence: petclinic-up workflow (2026-11-08); no run id recorded.

## ip timeout from ops

Cause: ops often reports "ip timeout" although the guest has an address. Two causes were seen. The arp lookup missed a guest that had already printed its address (suspected cause stated in nanovms/ops#1803, not verified; the arp fix is nanovms/ops#1805, merged). On an Italian locale the ops regex needs the English "at" in the output of arp -a; with LC_ALL=C it works. The second one is not isolated from a temporary iptables accept on ops0 and is not reported to ops.

Kind: gap in ops.

Also: ops compose up panics inside ops when the dns instance is late. The tool does not retry: it prints one ops compose up command (see the next steps it prints), so run it again if it fails. The CI workflows of the project run it up to three tries (docs/top30.md on the branch check/top30 records the tries per row that reached ops compose up).

## Hub packages

The hub package for postgres has no entrypoint: POSTGRES_DB, POSTGRES_USER, POSTGRES_PASSWORD and initdb.d are ignored, and ops compose up does not create the database. The tool writes post-up-<package>.sh next to compose.yaml, which creates the database and the user with psql, and prints the command to run it by hand once the instance is up (ops compose does not run it). Not measured: the script is covered by unit tests only, no CI run of it is recorded in this document. A second boot after a kill fails on a stale postmaster.pid. Data retention is not measured. The branch for POSTGRES_USER different from the superuser is unit-tested only.

## Init wrappers (tini, dumb-init)

Cause: many images start the program through a tiny init (ENTRYPOINT ["/sbin/tini", "--"], ["dumb-init", "--"]). The init starts the program as its child and waits for it. The observation took the init for the program (it is the only process that nobody started), the package ran tini, and tini cannot fork on nanos: the guest got an address, the console stayed empty and no port was open.

Kind: gap of the tool, fixed. The observation now passes over an init that has exactly one running child, as it does for a shell or a package manager launcher, and names it in the report ("init passed over").

Evidence: before, dashy (tini, program node server.js) and verdaccio (dumb-init) answered 000 x24 (dashy in run 45467614282, verdaccio in run 45462110286); the console had only the address line, and run 45485671510 measured every port from 1 to 10000 closed with nmap and package.manifest with Program /sbin/tini (dashy). After, run 45489113069 (tool commit 7a77d52): dashy 200 and verdaccio 200 on the first try. wallos also starts through dumb-init, but its command is startup.sh, which starts nginx, php-fpm and crond: see Process supervisors.

## Process supervisors (s6-svscan, runsvdir, supervisord)

Cause: some images start every service as a child of a supervisor. s6-svscan (gitea, gogs, kanboard) starts one process per service (web server, sshd, cron); nanos has no fork, so the supervisor cannot run. The consoles of gitea, gogs and kanboard were empty and ops reported ip timeout (runs 45462110286 and 45485671510).

Kind: hard limit for the supervisor. The tool prints "supervisor: <name> is a process supervisor ..." with the way out.

Way out, measured only where stated. Run the one server as the only process, with command: (and entrypoint: when the entrypoint of the image does not run its arguments) in a docker-compose.yml, or with --command SERVICE=CMD:
- gitea: command ["/usr/local/bin/gitea", "web"] with GITEA_I_AM_BEING_UNSAFE_RUNNING_AS_ROOT=true (the entrypoint of the image runs its arguments with exec). Measured: the front page answered 200 on 3000 on the first try, run 45489113069 (ci/top30-compose/gitea-web.yml on the check branch). The SSH server and the git commands that Gitea runs as child processes were not tried.
- kanboard: entrypoint ["php", "-S", "0.0.0.0:80", "-t", "/var/www/app"] (the entrypoint of the image ignores its arguments and ends in s6-svscan, so command: is not enough). Measured: it did not answer (console silent, every port from 1 to 10000 closed, instance Running), run 45489113069. The cause is not known (HYPOTHESIS: php -S does not get to listen on nanos; not diagnosed).
- wallos: command ["php", "-S", "0.0.0.0:80", "-t", "/var/www/html"] behind dumb-init. Measured: same silent result, run 45489113069. The original command (startup.sh) starts nginx, php-fpm and crond, which needs a fork each.
- gogs: its entrypoint start.sh runs its arguments, so command ["/app/gogs/gogs", "web"] is the candidate. Not tried. The image alone does not run in docker either without /data/gogs/conf/app.ini (docker run of the image, run 45485671510: FATAL custom config not found), so a config must be given by a bind mount.

Which programs the note names: every program whose file name starts with s6- (s6-svscan, s6-supervise, s6-linux-init, ...: the rule of isSupervisor in the survey fork of ops), runsv, runsvdir and supervisord, by the name of the program or of argv[0], wherever the file is (/usr/bin, /command of s6-overlay, a versioned folder of s6, /sbin or /usr/sbin for runit). A script that an interpreter runs is told only by the exact names (supervisord, s6-svscan, runsvdir, runsv), never by the s6- prefix, so a server that is given a path such as /etc/s6-overlay is not taken for a supervisor; so is supervisord when python runs it as a script or a module. The prefix is the only test: every s6-* name is taken for a tool of the suite, also those that are not supervisors at all (s6-tcpserver, s6-dns, s6-log, s6-setuidgid, ...), as the fork does. For these and for runsv the advice is softer: they are tools of a supervision suite, not necessarily the one that starts every service. Measured on real images: gitea, gogs and kanboard have /usr/bin/s6-svscan as their program and the report has the supervisor item (run 45578832813); the other names are covered by unit tests only. The list decides which program gets the supervisor note and which programs are never taken for the program of a shell by the nearest-program rule; it does not make the observation choose another program: gitea, gogs and kanboard still have /usr/bin/s6-svscan as their program (the report has the supervisor item and the "processes" item for each, compose up fails 3 of 3 with ip timeout). The tool does not go under a supervisor that is not a shell, so the one server has to be named by command: or --command, as above. Wallos is not an example of the note: its program there was busybox sh startup.sh (a shell script that starts three servers), for which only the "processes" item of the report appears.

The program under a shell that runs several things. A shell started with a command such as "(while :; do sleep 60; done) & java -jar x" has two running children, and the observation used to stop at the shell. The program is now the process nearest the shell that is not a shell (sh, bash, dash, ash, zsh, ksh, mksh), a supervisor (the list above, and nodemon and the like), ps, busybox (an applet of it), a command that only waits (sleep, tail, watch) or only copies what it is given (tee, cat, logger, rotatelogs, cronolog, svlogd, multilog), and of those at the same distance the one whose fork is the later one: the forks are compared on the way down from the shell, in the order the shell made them (the order on the command line; the line used is the one where the clone returned, the "<... clone resumed>" line when strace cut the call in two). What runs below the program is its own (a name resolver of a runtime, plugins it installs), and a setup step beside it comes first on the command line. Which of two children calls execve first is a race of the start and is never used. The waiters and the copiers are a primary filter, not a patch: the order of the forks does not help when the wait is last ("nginx & sleep infinity"), and a pipeline "server | tee log" forks the reader last. The distance counts the processes that executed a program between the shell and the process: a subshell that only forked (the loop above) is the shell, a shell that runs a command is a step. If every process under the shell is one of the kinds left out the shell stays. What is below a supervisor is the supervisor's own: in "supervisord -n & tail -f x" the program is supervisord (as for a shell that has only it) and nginx and php-fpm are not named. The rule is only for a shell with more than one running child; a shell, launcher or init with exactly one running child is passed over as before, and an init or a launcher with several keeps its place.

Not covered by the rule, known: a pipeline is told from "a & b" only by name, because the pipe and dup2 calls are not in the trace (adding them to the traced set needs a real strace format and a rule for dup2; not done), so "server | filter" with a reader that is not in the list above takes the reader for the program. A wrapper that forks and waits as the last branch ("a & timeout 5 b", su, sudo, runuser) is taken for the program, and what it starts is its own. A server in a wrapper script that does not exec ("redis-server & ./run.sh": redis is at distance 1, the gunicorn of the script at 2) loses to a helper started directly; the item names it. "( (loop) & exec server ) & worker": the loop is a child of the server's own branch, so it counts as the server's own and is not named.

What was not chosen is not dropped: the report has the item "also ran beside the program" with the names of the other programs that ran under the shell (not those below the chosen one, not shells), so "celery worker & gunicorn app" names the worker. An interpreter is named with its script, its -m module or its jar ("python3 app.py", "python3 -m celery", "java -jar app.jar"), by file name only. For a program that the hub replaces whole the names are in the item "also ran beside" of the hub items.

Origin: the rule is mainProgram of cmd/extract_from_dockerfile.go in the user's fork of ops (AngeloRubens/ops, branch survey/hub-categories), where it is tested on the table of "docker top". Differences: here it reads the strace trace, not a snapshot, so a process that has already exited is not a candidate, a thread that started a process (a Go program) is not a step (the starter of a process is the nearest process that executed a program), and "started last" is decided by the order of the forks of the shell, not by the execve calls or the pid (a snapshot has no such order; the fork takes the highest pid). The fork also leaves busybox out, and so does this one (a program run through busybox is not chosen). Tested on traces only (TestFinalExec, rows of the nearest-program rule). The row measured is below ("Measured").

If the observation keeps an init as the program (it has no child, or more than one), the report says so ("init kept"); unit-tested only.

## Programs that need a config file, variables or another service

Cause: some images do not start without what the user gives them in the compose file of the project: glance needs /app/config/glance.yml, reactive-resume needs APP_URL, AUTH_SECRET and DATABASE_URL (and a postgres), linkwarden needs DATABASE_URL (Prisma, error P1012), gogs needs app.ini. Converted from the Dockerfile alone (--dockerfile) there is nothing to give them. The same error is what docker prints when the image is run alone (docker run of the image, run 45485671510), so it is not nanos. The observation says it ("the container stopped with status 1 ... Log: ...").

Kind: not a limit of nanos or of the tool; use the docker-compose.yml of the project (-f), which has the bind mount and the variables. Measured: glance with a compose file that binds docs/glance.yml answers 200 on 8080 on the first try, run 45489113069. reactive-resume with the three variables and an unreachable DATABASE_URL starts the program and stops at the database ("Running database migrations", ECONNRESET 127.0.0.1:5432): it needs a postgres service next to it (not tried). linkwarden also needs a postgres (not tried).

## Files opened through a linked directory

Cause: pnpm keeps a package once in node_modules/.pnpm and links it (node_modules/x -> ../../node_modules/.pnpm/x/node_modules/x). The observation copied the files the program opened under the name of the link, which made a real directory with only those files in the package, and the program then did not find the others beside them (reactive-resume: ERR_MODULE_NOT_FOUND for dist/standard.js, console of run 45467614282; the package had env-core/dist/index.js and package.json as real files, package-files of run 45485671510).

Kind: gap of the tool, fixed: when the link is carried, what the directory holds is moved to the directory the link leads to and the link takes its place. The link is made, or the directory merged, only for a relative text that has ".." only at its start and leads inside the package as the directory really is on disk; an absolute text, a text that goes up again after a name (l/../x), a link that would lead out of the package, and a directory that holds a link with ".." or an absolute text are left as they are, with a line in the report. Measured: run 45489113069, the module error is gone and the program runs up to its own error (database connection).

## The home of the user of the image in the observation

Cause: the traced container starts as root and the program runs as the user of the image through strace -u; docker had set HOME=/root, where that user cannot write. directus (user node) runs pm2, which writes /root/.pm2: mkdir EACCES in the strace file of run 45485671510, the container stopped with status 1, and the observation took the last short command (node cli.js bootstrap) for the program.

Kind: gap of the tool, fixed: HOME is the home of the user in /etc/passwd of the image unless the image or the options set HOME. To read it the tool now creates one more container that is never started (and runs docker cp for /etc/passwd) for each image that runs as a named user; before, only a numeric uid was looked up. Measured: the observation of directus now ends with program node docker-entrypoint.cjs (run 45489113069). The row still does not answer: the console shows "futex op 3 not implemented" twice (FUTEX_REQUEUE, see above) and then nothing, and the entrypoint starts pm2-runtime, which forks. Which of the two stops it is not measured (HYPOTHESIS).

## A program started through a thread

Cause: a Go program (RoadRunner, rr, of shlink) starts its children from any of its threads. The thread has no execve of its own, so the observation did not see the children as children of rr, took the youngest php worker for the program, and the package ran bin/roadrunner-worker.php.

A thread that is not the leader and calls execve is written by strace as "T execve(... <pid changed to N ...>" then "N +++ superseded by execve in pid T +++" and "N <... execve resumed>) = 0" (read from a real trace of strace 5.16 made by a reviewer; not captured in a CI run of this project). The tool had read both lines the other way round and dropped the execve; it now reads them as strace writes them.

Kind: gap of the tool, fixed: the starter of a process is the nearest process that executed a program (also for the commands reported as not run and for the launchers a program started). Measured: shlink has rr serve -c config/roadrunner/.rr.yml as its program (run 45492200295). The row still does not answer, see the next section and No fork.

## The working directory of the program

Cause: the package took the working directory from /proc/<pid>/cwd at the end of the observation, where the program is, not where it started. rr starts in /etc/shlink (WORKDIR of the Dockerfile, and the entrypoint ends in exec ./bin/rr serve -c config/roadrunner/.rr.yml) and changes to the folder of its config; the manifest then had cwd /etc/shlink/config/roadrunner and rr stopped with "chdir /etc/shlink/config/roadrunner/config/roadrunner: no such file or directory" (console of run 45492200295). That was a bug of the tool, not of nanos.

Kind: gap of the tool, fixed: the directory is the one the process has at its execve line, read from the trace (chdir calls followed, processes cloned with CLONE_FS sharing one directory, as in the kernel), and /proc is used only when the trace does not know it. Known gap: a relative chdir("..") after a chdir through a symbolic link is followed lexically, where the kernel goes to the parent of what the link leads to. Not modelled either: unshare(CLONE_FS) (a chdir is wrongly shared afterwards), chroot and setns (paths are read against the wrong root). A thread or clone shares the directory only with CLONE_FS, as the kernel does. Measured: the manifest of shlink now has cwd /etc/shlink and rr gets as far as starting its plugins (run 45496410983). The row is still red: rr then stops with "jobs_plugin_serve: WorkerAllocate: static_pool_allocate_workers: fork/exec /usr/local/bin/php: function not implemented", and compose up reports ip timeout because the guest ends. That is the no-fork limit: RoadRunner starts a php process for each worker, and there is no single-process mode.

## The version of the build comes from git tags

Cause: syncthing computes its version from git describe in the build (go run build.go). The workflow cloned with depth 1 and no tags, so the version was the commit id, and the program refused to start: "Invalid version string 992ae894" in docker as well. Not a limit of the tool or of nanos.

Fix: the workflow fetches the history and the tags for that row (blobless). Measured: run 45485671510, syncthing answers 200 on 8384. If a Dockerfile needs git metadata, build from a full clone.

### Measured: the program under a shell with a loop and a server

A row built for this rule, ci/nearest/Dockerfile on the check branches: python:3.12-alpine with CMD ["sh", "-c", "(while :; do sleep 60; done) & python3 -m http.server 8000 --bind 0.0.0.0 & wait"], converted with --dockerfile as it is, probed on 8000.
- Without the rule (feat/modules, check/top30-nearest-before, run 45582803752): the program is busybox sh (2 arguments, the shell), the guest has an address, the console shows nothing, 8000 answers 000 x24.
- With the first version of the rule (run 45582811522): the program is the sleep of the loop, 000 x24. In the real trace the server is exec'd (line 17) before the sleep of the loop subshell (line 23), so "the latest at the same distance" is the sleep. That is why sleep and tail are not taken for the program.
- With the rule as it is now (check/top30-nearest-after, run 45584598066, dock2nano fcc7593): the program is /usr/local/bin/python3.12 with its 5 arguments and the row answers 200 on 8000 at the first try; the console shows the request of the probe.
- With the tie decided by the order of the forks (check/top30-nearest-after, run 45587576129, dock2nano a66acec): the same program (python3.12) and 200 on 8000 at the first try; the report has the item "also ran beside the program: sleep".
A first version of the row, without the "& wait", answered 200 also without the rule (runs 45581649928 and 45581655781): the shell of busybox runs the last command of -c in its own process, so no shell was left. Only the second form is a case of the rule. One image and one shell (busybox ash) were measured; a java server, or bash, were not.
