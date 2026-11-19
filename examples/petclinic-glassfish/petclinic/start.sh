#!/bin/sh
# Start GlassFish, create the JDBC pool and resource from the environment,
# deploy the WAR, then keep the server in the foreground.
set -e

: "${DB_HOST:=db}"
: "${DB_NAME:=petclinic}"
: "${DB_USER:=petclinic}"
: "${DB_PASSWORD:=petclinic}"

ASADMIN="$GLASSFISH_HOME/bin/asadmin"

"$ASADMIN" start-domain domain1

# The colon in the URL must be escaped for asadmin property lists; using the
# separate serverName/portNumber properties avoids it.
"$ASADMIN" create-jdbc-connection-pool \
  --restype javax.sql.DataSource \
  --datasourceclassname com.mysql.cj.jdbc.MysqlDataSource \
  --property "serverName=${DB_HOST}:portNumber=3306:databaseName=${DB_NAME}:user=${DB_USER}:password=${DB_PASSWORD}:useSSL=false:allowPublicKeyRetrieval=true" \
  petclinicPool

# Unsure: ping can fail if the db is still starting; not fatal.
"$ASADMIN" ping-connection-pool petclinicPool || true

"$ASADMIN" create-jdbc-resource --connectionpoolid petclinicPool jdbc/petclinicDS

"$ASADMIN" deploy --name petclinic --contextroot / /opt/petclinic.war

# Restart in the foreground so the container stays up on the server process.
"$ASADMIN" stop-domain domain1
exec "$ASADMIN" start-domain --verbose domain1
