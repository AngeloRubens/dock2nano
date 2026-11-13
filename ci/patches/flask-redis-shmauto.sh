# Variant F: nothing is done for /dev/shm here: the tool has to have made the directory.
PROJECT=flask-redis
. "$(dirname "$0")/lib.sh"
f="$(root web)/code/app.py"
sed -i "s#host='redis'#host='redis.service'#" "$f"
grep -n "Redis(\|multiprocessing" "$f"
ls -ld "$(root web)/dev/shm" 2>&1
