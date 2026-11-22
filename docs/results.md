# Measured results

Only what was measured is listed. Anything not measured is "not run". Run ids are GitHub Actions runs on the fork of the project. "id not recorded" means the project notes states the result but not the run id. Dates are those of the project notes entry that reports the result.

| Case | Outcome | Run id | Date | Remark |
| --- | --- | --- | --- | --- |
| compose parser, awesome-compose files | 39 of 39 parse | id not recorded | 2026-11-07 | parser only, no conversion |
| petclinic (GlassFish, MySQL) with three hand changes | answers 200 (home, owner, petType, veterinarian) | id not recorded | 2026-11-08 | workflow petclinic-up, about 7 minutes startup |
| petclinic without -Xint | red (JVM crash) | id not recorded | 2026-11-08 | 5 of 5 runs failed |
| flask-redis | answers 200 | id not recorded | 2026-11-10 | needs dev/shm, debug=False, redis.service |
| nginx-flask-mongo | backend and mongo work, nginx red | id not recorded | 2026-11-10 | nginx fails to resolve backend.service |
| npm start images | red | id not recorded | 2026-11-10 | npm forks, lazy modules not carried |
| sparkjava backend | resolved db, then failed | id not recorded | 2026-11-10 | missing tzdb.dat |
| compose-up on ops master 1183538 | 7 jobs green | id not recorded | 2026-11-13 | red: sparkjava-mysql without flags, react-express-mongodb (nodemon), nginx jobs |
| compose-up, PR text | 3 of 10 green | id not recorded | 2026-11-13 | nginx jobs red by nanovms/ops#1813 |
| nginx-golang, local run with KVM forced | red, 3 of 3 | 43510838986 | 2026-11-14 | "host not found in upstream backend.service"; artifact of this run used; KVM does not fix it |
| nginx-golang with exec_wait_for_ip4_secs 30 | compose up ok at attempt 1, proxy and backend answer 200 | 44583469624 | 2026-11-19 | one run |
| nginx-golang without the option | compose up failed, 3 of 3 | 44583469624 | 2026-11-19 | same job without the option |
| nginx-golang with and without exec_wait_for_ip4_secs | 3 of 3 with, 3 of 3 without (outcomes as stated in the project notes) | 44583469624, 44612626939, 44612633466 | 2026-11-19 | the project notes gives the counts for the three runs together; the first run is described in the two rows above |
| compose-up matrix, ops master 2177c54, address wait | 8 of 10 green | id not recorded | 2026-11-20 | red: nginx-nodejs-redis, react-express-mongodb |
| ops#1816 cwd leak | old ops fails with "unable to change cwd", master 2177c54 starts | id not recorded | 2026-11-20 | |
| nginx-nodejs-redis web1 with FUTEX_REQUEUE kernel | 4 of 4 start | id not recorded | 2026-11-20 | on the fork branch of nanos |
| nginx-nodejs-redis web1 with nightly kernel | 0 of 4 start | id not recorded | 2026-11-20 | earlier revision |
| nanos mmap test, origin/master | hangs: 0 of 30 (1 vcpu), 7 of 30 (2), 6 of 30 (4) | id not recorded | 2026-11-20 | hang counts as stated in the project notes; 18 of 60 on master against 16 of 60 on the branch with 2 vcpus |
| spring-postgres with hub postgres | 200 after CREATE DATABASE | id not recorded | 2026-11-21 | second boot after kill fails on stale postmaster.pid |
| feat/modules at 542bb5d, ci | green | 44920078254 | 2026-11-21 | |
| feat/modules at 542bb5d, integration | green | 44920078564 | 2026-11-21 | |
| fastapi, original command (3 identical jobs) | red, answers 000 | 44808943029 | 2026-11-22 | shebang fix works; gunicorn then dies with os.fork() OSError 38 |
| fastapi, Args changed after conversion | red | 45003079974 | 2026-11-22 | "No module named runpy" |
| fastapi, uvicorn command added before conversion | answers 200, 3 of 3 | 45003684361 | 2026-11-22 | ci/patches/fastapi.pre.sh |
| 12 Java servers on the fork CI | started, not stable | id not recorded | not recorded | ActiveMQ, Helidon, WildFly sometimes crash under emulation |
| petclinic after the ops dns fix 1803 | not shown | not run | not recorded | do not claim it |
| data retention of postgres | not run | not run | not recorded | |
| django, runserver --noreload | not run | not run | not recorded | |
| hub apache and php rows | not run | not run | not recorded | not in the table yet |
