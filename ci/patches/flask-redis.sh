# Flask with debug=True starts a second process (the reloader); python needs /dev/shm.
PROJECT=flask-redis
. "$(dirname "$0")/lib.sh"
r="$(root web)"
f="$r/code/app.py"
sed -i "s#host='redis'#host='redis.service'#; s#debug=True#debug=False#" "$f"
grep -n "Redis(\|debug" "$f"
