#!/bin/bash
# Experiment: create database, role and password in the db guest from outside, after compose up.
D=/tmp/ac/spring-postgres
O=/tmp/out
sudo env OPS_HOME="$OPS_HOME" ops instance list 2>&1 | tee $O/pginit-instances.txt
sudo env OPS_HOME="$OPS_HOME" ops instance logs db > $O/console-db.txt 2>&1 || true
ip=$(grep -o 'assigned 192\.168\.33\.[0-9]*' $O/console-db.txt | tail -1 | awk '{print $2}')
echo "db ip: $ip"
[ -z "$ip" ] && { echo "no db address"; exit 0; }
echo "--- compose env names of db (values of the password never printed)"
DB=$(grep -o 'POSTGRES_DB=[^ ]*' $D/compose.yaml | head -1 | cut -d= -f2)
USR=$(grep -o 'POSTGRES_USER=[^ ]*' $D/compose.yaml | head -1 | cut -d= -f2)
grep -o 'POSTGRES_[A-Z_]*' $D/compose.yaml | sort -u
echo "POSTGRES_DB value: $DB; POSTGRES_USER value: ${USR:-<unset, default postgres>}"
PW=$(tr -d '\r\n' < $D/db/password.txt)
echo "password file db/password.txt: $(wc -c < $D/db/password.txt) bytes"
sudo apt-get install -y -qq postgresql-client >/dev/null 2>&1; psql --version
for i in $(seq 1 30); do
  timeout 3 bash -c "exec 3<>/dev/tcp/$ip/5432" 2>/dev/null && { echo "port 5432 open after try $i"; break; }
  sleep 5
done
export PGCONNECT_TIMEOUT=10
run() { echo "--- psql $*"; psql -w "$@" 2>&1 | sed "s/$PW/<pw>/g"; echo "[exit ${PIPESTATUS[0]}]"; }
echo "=== without a password"
run -h $ip -U postgres -d postgres -c '\du'
run -h $ip -U postgres -d postgres -c '\l'
run -h $ip -U postgres -d postgres -c 'show hba_file' -c 'show listen_addresses'
run -h $ip -U postgres -d postgres -c 'select type,database,user_name,address,auth_method from pg_hba_file_rules'
echo "=== with the compose password"
export PGPASSWORD="$PW"
run -h $ip -U postgres -d postgres -c '\du'
unset PGPASSWORD
echo "=== create"
if [ -n "$USR" ]; then
  run -h $ip -U postgres -d postgres -c "CREATE ROLE \"$USR\" WITH LOGIN SUPERUSER PASSWORD '$PW'"
  run -h $ip -U postgres -d postgres -c "CREATE DATABASE \"$DB\" OWNER \"$USR\""
else
  run -h $ip -U postgres -d postgres -c "CREATE DATABASE \"$DB\""
  run -h $ip -U postgres -d postgres -c "ALTER ROLE postgres PASSWORD '$PW'"
fi
echo "=== after"
run -h $ip -U postgres -d postgres -c '\l'
run -h $ip -U postgres -d postgres -c '\du'
echo "--- connect to $DB with the password"
PGPASSWORD="$PW" run -h $ip -U "${USR:-postgres}" -d "$DB" -c 'select current_user, current_database()'
echo "--- pg_hba after the password change"
run -h $ip -U postgres -d postgres -c 'select 1'
