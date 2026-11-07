Jakarta EE PetClinic on GlassFish 7.0.5 with MySQL 8.4, started by docker compose.

Source: https://github.com/Jakarta-EE-Petclinic/petclinic-jakartaee
(license EPL-2.0), pinned at commit 242b5d79724ba4ce51168b713a814b47c3f6675b.

This is written the way a normal user would write it. It is not adapted to
nanos. The purpose is to see what the tool converts and what it reports.

Files
  docker-compose.yml     db (mysql:8.4, named volume, healthcheck) and
                         petclinic (build ./petclinic, port 8080, waits for db)
  petclinic/Dockerfile   stage 1 builds the WAR with ./mvnw -Pglassfish
                         -DskipTests package; stage 2 is GlassFish on a JRE
  petclinic/start.sh     at container start: creates the JDBC pool and the
                         resource jdbc/petclinicDS from DB_HOST, DB_NAME,
                         DB_USER, DB_PASSWORD, deploys the WAR, runs GlassFish
                         in the foreground

Things not verified
  - Whether the glassfish profile needs more than skipping tests. If the build
    fails, the Dockerfile needs more options here.
  - The WAR name; the Dockerfile takes the first target/*.war.
  - The asadmin pool properties and the stop/start restart in start.sh.

The workflow .github/workflows/petclinic.yml runs only convert, on manual
dispatch or on a commit message containing [petclinic]. The report is the
result, failures included.
