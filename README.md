# dock2nano

dock2nano moves a project that is built and run with Docker or docker compose to nanos. You keep your Dockerfile and your docker-compose.yml as they are. The tool reads them, builds the images with docker, runs each service for a while and watches what it uses, writes one nanos package per service, and writes a compose.yaml that "ops compose" runs.

The name of the tool is a working name (the constant ToolName in cmd/name.go).

What the tool writes is what it observed, not a proof that the program works on nanos: review it. What it cannot convert is printed in the report, never dropped in silence. The goal is zero impact on your files; the tool aims at it and does not guarantee it.

## Requirements

- docker, running on the machine where you convert. The images are built and observed with docker.
- ops, to run the result. For "ops compose" you need an ops built from master since commit 2177c54. Bare service names (db instead of db.service) in compose.yaml need ops master since 2177c54 (no release up to 0.1.47); with a released ops write <package>.service (see --short-names in the usage text and docs/limits.md, section "ops-dns answers only .service names").
- sudo to run "ops compose up" (it creates network interfaces and starts qemu).

The tool does not run ops. It prints the ops commands to run (below).

## Quick start

From the directory of a docker-compose.yml (the tool looks for docker-compose.yml, docker-compose.yaml, compose.yml, compose.yaml, in this order):

    dock2nano convert

or name the file and the output directory:

    dock2nano convert -f docker-compose.yml -o out --pull

With --pull the images that are not on the machine are pulled. At the end the tool prints the report and a "Next steps" block with the commands to run, in this shape:

    OPS_HOME=<home> ops pkg get <the dns package>    (once, if you do not have it yet)
    sudo env OPS_HOME=<home> PATH="$PATH" ops compose up -f <absolute path of compose.yaml>

(the OPS_HOME prefix is left out when no home is known; the paths are absolute.)

Run those commands as printed. The full list of flags is the usage text of the tool; run dock2nano without arguments to read it. An example with two services (nginx and an app) is in examples/two-services.

## The two inputs

1. A compose file: dock2nano convert -f docker-compose.yml. Every service with a build section is built with docker; every service with an image is taken from this machine (--pull to pull it). --service NAME converts only that service.
2. One Dockerfile: dock2nano convert --dockerfile path/to/Dockerfile. The Dockerfile is converted as a single service. --context DIR sets the build context (default: the directory of the Dockerfile) and --name NAME sets the service and project name (default: the name of the context directory). --dockerfile cannot be used with -f.

## How long the observation lasts (--seconds)

Each service is observed for 30 seconds by default (--seconds N). Raise it for a program that starts slowly, so that the observation sees the code that runs after the start. The CI of the project that converts the sample projects of docker/awesome-compose uses --seconds 90 (see .github/workflows/compose-up.yml). Not measured: the lowest value that is enough for a given program; no run compares 30 with 90 seconds, so the choice of 90 is not backed by a comparison in docs/coverage.md. The tool also cuts the time short when all the ports have been asked and nothing new shows up (see internal/observe).

## What the report means

The report lists, per service, what was converted, approximated or not converted. These notes are the ones people ask about most:

- "port N could not be asked for /" (a warning on stderr): during the observation the tool asks each port the program listens on, or the image exposes, for its front page, so that the files the program opens only to answer are seen. This port did not answer (for example connection refused, because the program was not listening yet or does not speak HTTP). What to do: if the program needs a request to load its code, run again with --exercise to say what to ask (a path or a port), or --exercise-cmd for a program that does not speak HTTP; raise --seconds if the program was still starting.
- "unsupported processes": the program started child processes (fork or clone) while it was observed. Nanos runs one process and has no fork, so the program may not start. What to do: look at the option of the program to run in a single process (no master process, a single worker), and give that command with --command SERVICE=CMD. See "No fork in nanos" in docs/limits.md.
- "note expose": expose in the compose file and EXPOSE in the image do not publish a port; only a port with a published value (ports: in the compose file) is published. What to do: if the service must be reached from the host, add a ports: entry. Services reach each other by name through ops-dns without it.
- "unsupported dockerfile: not converted: line N: COPY --link" (and the same for any other instruction or option the Dockerfile parser does not model): the tool does not read that instruction or option for the report. The image was still built by docker and observed as it is, so the files come from the observation, but anything the report would take from that line (environment, user, working directory) can be missing. What to do: check by hand the values that the instruction sets.
- Other lines you will see: unsupported items for the parts that nanos or ops compose cannot do (healthchecks, networks other than the default, replicas, volumes that are not simple), and "approximated" items for the guesses of the tool (for example the user of the program). They are described in docs/limits.md. A report with an unsupported item makes the exit status 2 with --strict.

## When docker build fails

If docker build fails, convert exits with status 1 and prints docker's own error. For two common causes it adds a hint below the error: a COPY or ADD source that is not in the build context (it does not exist, or .dockerignore excludes it; many Dockerfiles expect a file made by make, goreleaser or npm before docker build: run that step first, then convert again), and a RUN step that fails (the build runs with --quiet, so the hint says to run docker build without --quiet to see the output of the step; a common cause is an ARG without a default that is not given: VERSION in datasette and TARGET in redlib were empty in the real runs). When the failing instruction is COPY --from=<stage or image>, the hint is a different one: the stage or image named in --from has no such path, so check the path that the earlier stage writes. Run id of the observation of these failures: 45439008280 (docs/top30.md on the branch check/top30, 11 Dockerfiles). The hint itself is covered by unit tests only; no CI run of it is recorded here.

## A database from the hub (postgres)

For a program that did not start as one process, the tool can take the environment of the package of the same program from the hub of ops packages (https://repo.ops.city), unless you give --no-hub. The hub package of postgres has no entrypoint: POSTGRES_DB, POSTGRES_USER, POSTGRES_PASSWORD and initdb.d are ignored. For that case the tool writes a script, post-up-<package>.sh, next to compose.yaml and prints a line "then, once <package> is up: sudo --preserve-env=POSTGRES_PASSWORD,PGHOST,POSTUP_TRIES env OPS_HOME=<home> PATH="$PATH" sh <absolute path of post-up-<package>.sh>" (without OPS_HOME when no home is known). ops compose up does not run it: you run it yourself, after compose up, with the password in the variable POSTGRES_PASSWORD of your shell (export it first). The script creates the database and the user with psql (not measured: unit tests only; no CI run of the generated script is recorded here). See "Hub packages" in docs/limits.md.

## The probe used in CI

The CI of the project checks a converted project with a strict probe: after ops compose up it asks the address of the instance (from ops instance logs) on the exposed port, up to 24 times, 10 seconds apart. Green means an HTTP status 2xx or 3xx, or an open TCP port; a 4xx or 5xx answer is "reached, not a success"; no answer is red. See docs/coverage.md.

Which port to probe when EXPOSE uses a variable (EXPOSE $PORT): not measured. What the code does: the tool takes the exposed ports from the image as docker reports them, and during the observation it also asks every port the program listens on; the Dockerfile parser keeps a variable in EXPOSE as written. Runs of Dockerfiles with a variable in EXPOSE are recorded in docs/top30.md on the branch check/top30 (run 45432518617): homepage (EXPOSE $PORT) was reached, with 400 x24; verdaccio (EXPOSE $VERDACCIO_PORT) got no answer; http-echo (EXPOSE $HTTP_PORT $HTTPS_PORT) answered 200 on 8080, the first port. Not measured: the general rule of which port to pick.

## More

- docs/limits.md: what does not convert or does not run on nanos, why, and the evidence.
- docs/coverage.md: the matrix of docker/awesome-compose samples run in CI.
- docs/manual-changes.md and docs/results.md: changes needed by hand and results.
