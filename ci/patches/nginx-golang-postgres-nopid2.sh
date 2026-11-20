# Control for the postgres cases: the observation carried postmaster.pid, postmaster.opts and the socket lock
# of the server that was running when the observation ended. Remove them from the package of the db.
PROJECT=nginx-golang-postgres
. "$(dirname "$0")/lib.sh"
r="$(root db)"
echo "before:"; find "$r" \( -name postmaster.pid -o -name postmaster.opts -o -name '.s.PGSQL.*.lock' \) -print
find "$r" \( -name postmaster.pid -o -name postmaster.opts -o -name '.s.PGSQL.*.lock' \) -delete
echo "after:"; find "$r" \( -name postmaster.pid -o -name postmaster.opts -o -name '.s.PGSQL.*.lock' \) -print; echo "(none above = removed)"
