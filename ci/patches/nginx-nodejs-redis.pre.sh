# Before the conversion: the web service source of awesome-compose is at
# /tmp/ac/nginx-nodejs-redis/web (the workflow clones docker/awesome-compose to /tmp/ac and the
# compose file builds ./web). Its .npmrc says ignore-scripts=true, and with npm 6 (node 14.17.3)
# "npm start" then runs no script and exits 0: the example is broken under plain Docker too
# (docker compose up leaves web1 and web2 at "Exited (0)"; run 42994092518 on check/docker-npm,
# whose control run without the .npmrc starts node server.js and answers). Without this change the
# job could only show that. The file is emptied, not deleted, because the Dockerfile has
# "COPY .npmrc ." and a missing file would fail the build.
f=/tmp/ac/nginx-nodejs-redis/web/.npmrc
test -f "$f"
: > "$f"
ls -la "$f"
