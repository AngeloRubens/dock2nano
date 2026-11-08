Jakarta EE PetClinic on GlassFish 7.0.5 with MySQL 8.4, started by docker compose.

Source: https://github.com/Jakarta-EE-Petclinic/petclinic-jakartaee
(license EPL-2.0), pinned at commit 242b5d79724ba4ce51168b713a814b47c3f6675b.

This is written the way a normal user would write it. It is not adapted to
nanos. The purpose is to see what the tool converts and what it reports.

Files
  docker-compose.yml     db (mysql:8.4, named volume, healthcheck) and
                         petclinic (build ./petclinic, port 8080, waits for db)
  petclinic/Dockerfile   stage 1 builds the WAR with mvn -B -q -Pglassfish
                         -DskipTests package (mvn, not ./mvnw: the maven image
                         sets MAVEN_CONFIG, which the wrapper reads as a build
                         phase); stage 2 is GlassFish on a JRE
  petclinic/start.sh     at container start: creates the JDBC pool and the
                         resource jdbc/petclinicDS from DB_HOST, DB_NAME,
                         DB_USER, DB_PASSWORD, deploys the WAR, runs GlassFish
                         in the foreground

Workflows
  .github/workflows/petclinic.yml     runs only convert, on manual dispatch or
                                      on a commit message containing
                                      [petclinic]. The report is the result,
                                      failures included.
  .github/workflows/petclinic-up.yml  converts, starts the result with
                                      "ops compose up" and reports how far it
                                      gets, on manual dispatch or on a commit
                                      message containing [petclinic-up]. It
                                      asserts nothing: the output is the
                                      finding.

Verified (GitHub Actions, run of .github/workflows/petclinic-up.yml)
  - The example runs with plain docker compose (petclinic-docker.yml): home,
    owner, petType and veterinarian pages answer 200.
  - Converted by the tool and started with "ops compose up": the same four
    pages answer 200 on nanos, GlassFish reads the tables of MySQL, and no SQL
    or connection error is in the console. Startup takes about 7 minutes.
  - This needed three changes made by hand after the conversion, each one named
    by the report or by the console (they are in the "review the report" step
    of petclinic-up.yml):
      1. mysqld refuses to run as root and nanos runs everything as root: the
         arguments of the db package get --user=root. The report says: "the
         program ran as uid 999 ... a program that refuses to run as root ...
         may not work".
      2. mysqld looks up the name of each client; the DNS of "ops compose"
         does not answer it and the connection of GlassFish fails ("Communications
         link failure"): --skip-name-resolve.
      3. In the JDBC pool of GlassFish (domain.xml in the sysroot of the
         petclinic package) the host db became db.service: on ops a service is
         reached as <pkg>.service, which the report notes.
  - The JVM crashes at random places (SIGSEGV in compiled code or in libjvm)
    on nanos unless it runs with -Xint (interpreter only): 4 of 5 runs without
    it crashed or failed to start, 3 of 3 with it started. -Xint is added to
    the arguments of the petclinic package in the same step. It is slow.
  - The schema is shipped as initdb/schema.sql, mounted in
    /docker-entrypoint-initdb.d. The application creates its tables when it is
    deployed (asadmin deploy in start.sh), and the package of the database is
    copied before that: what a dependent service writes into a dependency is
    not in the package of the dependency. The tool says so in the report of the
    dependency.

Not verified
  - The asadmin pool properties and the stop/start restart in start.sh were
    only exercised by the runs above.
  - Data: the pages show empty lists; the sample data of the project is not
    loaded.
