# Nothing is changed by hand: this only shows what the db package carries of its data directory.
PROJECT=sparkjava-mysql
. "$(dirname "$0")/lib.sh"
d="$(root db)/var/lib/mysql"
echo "data directory of the db package:"
ls -la "$d" 2>&1 | head -40
echo "example:"
ls -la "$d/example" 2>&1 | head
grep -a -c . "$d/ddl_recovery.log" 2>/dev/null
