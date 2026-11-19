# Variant A: the same changes as flask-redis, without the empty /dev/shm directory.
PROJECT=flask-redis
. "$(dirname "$0")/lib.sh"
f="$(root web)/code/app.py"
sed -i "s#host='redis'#host='redis.service'#; s#debug=True#debug=False#" "$f"
ls -la "$(root web)/dev" 2>&1 | head -3
