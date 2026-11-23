# Manual changes

The goal is zero changes by the user. These are the hand changes that were still needed in the cases tried, and whether each one became a tool feature or is still manual. The output of the tool is what was observed and has to be reviewed.

## Summary

| Case | Change | Status |
| --- | --- | --- |
| fastapi | command uvicorn added to the compose file before the conversion | tool option: convert --command 'api=uvicorn app.main:app --host 0.0.0.0 --port 8000' (the file stays unchanged); the user has to give it, the tool does not guess it |
| petclinic | mysqld args --user=root --skip-name-resolve | manual |
| petclinic | domain.xml serverName db changed to db.service | manual; the flag --short-names (dns_search) exists since later |
| petclinic | JVM -Xint | manual |
| petclinic | schema shipped as initdb/schema.sql | example file, manual |
| flask-redis | dev/shm directory, debug=False, redis.service | patch in the compose-up workflow, manual |
| nginx-nodejs-redis | none possible in the tool | red, nanos lacks FUTEX_REQUEUE |
| postgres | hub package, CREATE DATABASE by hand | partly a tool feature |

## fastapi (awesome-compose)

Change: run `convert --command 'api=uvicorn app.main:app --host 0.0.0.0 --port 8000'`. The command replaces the one of the service before the observation, so the observation and the package run it. Changing only the Args of the package after the conversion fails (No module named runpy), because the observation never saw uvicorn. The report says the command was replaced and prints the program and the number of arguments, not the arguments. An entrypoint of the service or of the image still runs and receives the command as arguments. A hub package used whole does not run the command.

Why: the original command runs gunicorn with uvicorn workers, and gunicorn calls os.fork(), which nanos does not have (run 44808943029, 3 of 3 red at the probe with OSError 38).

Why before the conversion: changing only Args in the manifest after the conversion failed with "No module named runpy" (run 45003079974). The observation of gunicorn never reached uvicorn or runpy, so the files are not in the package. The override has to be seen by the observation.

Result with the change: answers 200, 3 of 3 (run 45003684361, 2026-11-22).

Report: when gunicorn with the worker class uvicorn.workers.UvicornWorker is observed and starts child processes, the item "processes" now gives the command to copy (`--command 'api=uvicorn MODULE:APP --host H --port P'`, taken from the command line; the host and port are said to be assumed when no --bind was given). Measured: fork run 45223574856 (the fastapi suggestion, gunicorn to uvicorn, with host and port assumed); ci 45223366657 and integration 45223366390 green. The command now ends with --workers 1 and carries --chdir as --app-dir. Not measured: uvicorn --workers and --reload, WEB_CONCURRENCY.

Status: manual. The real feature is a tool option that overrides the service command and is used in the observation. It is not built. An earlier fix, to use the command line of /proc when the program was started through a shebang (commit f67751e), works but is not merged into feat/modules at the time of the project notes entry.

## petclinic (GlassFish and MySQL)

The converted packages run on nanos once three changes are made in the review step (workflow petclinic-up, about 7 minutes of startup; home, owner, petType and veterinarian pages answered 200 with no SQL errors; 2026-11-08, no run id recorded).

1. mysqld arguments --user=root --skip-name-resolve. Reason: nanos runs as root and mysqld refuses root.
2. domain.xml serverName db changed to db.service. Reason: a service named db is db.service on ops. Since then the tool has --short-names (dns_search), which makes the short name resolve; it was verified on another example (sparkjava), not re-run on petclinic.
3. JVM option -Xint. Reason: compiled code crashed the JVM at random places on nanos (5 of 5 runs without -Xint failed).

Also: the app creates its tables on asadmin deploy, after the db package is copied, so the schema is shipped as examples/petclinic-glassfish/initdb/schema.sql.

Status: all three still manual. Asking for them to become tool features (for example an --arg option) was left as an option. Tool changes that came from this case are in: base_volume_sz grows with the files (mysqld said "No space left") and the report warns that what dependents wrote into a dependency is not in its package.

Nanovms/ops#1803 was not shown to fix petclinic.

## flask-redis

Needed (compose-up workflow patch): a dev/shm directory, debug=False in the app, and the host name redis.service.

Result: answers 200 on nanos (2026-11-10, no run id recorded).

Status: manual, as hand patches in ci/patches. The machine files and anonymous volumes handling in the tool came from this round of work.

## nginx-nodejs-redis

No hand change makes it work. The node web1 service needs FUTEX_REQUEUE, which nanos lacks (musl pthread_cond_broadcast, Alpine node:14). Red in the matrix of 2026-11-20. See limits.md.

The nginx part (resolving the upstream at start) was a manual manifest option and became a tool feature: the tool writes exec_wait_for_ip4_secs "30" for every service with depends_on on a converted service.

## nginx-flask-mongo and nginx-golang

The nginx exit "host not found in upstream backend.service" needed exec_wait_for_ip4_secs in the proxy manifest. That is now written by the tool (measured on nginx-golang, runs 44583469624, 44612626939, 44612633466). See limits.md.

## sparkjava-mysql

Red without flags (known red in the matrix of 2026-11-13). With --short-names the backend resolved db, then failed on a missing tzdb.dat because the observation never reached that file. Still manual or open.

## postgres (hub package)

Observe first, use the hub only as a fallback for recipes, --no-hub turns it off. For the postgres image the hub package is used whole (hints/hub-required.yaml, ApplyHubWhole in internal/gen/hub.go). Two hub packages are listed (versions 11.3.0 and 16.2).

Hand steps that remain:
1. The hub package has no entrypoint: POSTGRES_DB, POSTGRES_USER, POSTGRES_PASSWORD and the initdb.d files are ignored. The tool does not create the database; it prints the psql CREATE DATABASE command to run by hand.
2. spring-postgres answered 200 after CREATE DATABASE (2026-11-21, measured).
3. A second boot after a kill failed on a stale postmaster.pid (measured). Data retention is not measured.

Status: the hub use is a tool feature, the database creation is manual.

## Hub apache and php

The hub apache listens on 8080 with MapDirs of the docroot, and the hub php package is php -S. These rows are not in the table of the tool yet (project notes, 2026-11-21).

## django (awesome-compose)

Changes: run `convert --command 'web=manage.py runserver 0.0.0.0:8000 --noreload'` (the autoreloader of runserver starts another process, and nanos has no fork), and make the observation ask the application with an allowed Host header: `--exercise-cmd 'web=curl -s -o /dev/null -H "Host: localhost" http://$DOCK2NANO_HOST:$DOCK2NANO_PORT/admin/login/'`. Without the request, django.contrib.messages.storage.fallback is not carried and the first valid request fails with 500 (--exercise with a path did not help: the tool sends the address of the container as Host, which ALLOWED_HOSTS rejects; this cause is a hypothesis that the 200 above is consistent with, not isolated by a separate run). Measured: run 45076955146, one run, answers 200. Status: tool option, the user gives the host (the tool does not guess it). The option `--exercise-host [SVC=]HOST` sets the Host header of the requests of --exercise. Measured with `--command 'web=manage.py runserver 0.0.0.0:8000 --noreload' --exercise web=/ --exercise web=/admin/login/ --exercise-host web=localhost:8000`: run 45078461680, 3 of 3 jobs answer 200; the unit tests are green in run 45078373869.

Report: when runserver is observed without --noreload and starts child processes, the item "processes" now gives the `--command` with --noreload added to the observed command line. Measured: fork run 45223574856 (the django suggestion, --noreload); ci 45223366657 and integration 45223366390 green. Not measured: WEB_CONCURRENCY and the other servers.

Report: a request of --exercise answered with 400 or 403 and a body with DisallowedHost or Invalid HTTP_HOST header is now a warning that says the Host header was rejected and names `--exercise-host web=localhost`; any status of 400 or more is a warning with the status. The body is not printed. Measured: fork run 45223574856 (django with --command alone printed the Host rejection warning naming --exercise-host). Not measured: the 400 body of Django with DEBUG off, webpack, Rails, Vite.
