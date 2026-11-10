# Variant F: multiprocessing in use and nothing done for /dev/shm: the tool has to make the
# directory itself. The app is changed as in flask-redis-shmuse.
PROJECT=flask-redis
. "$(dirname "$0")/lib.sh"
f="$(root web)/code/app.py"
sed -i "s#host='redis'#host='redis.service'#; s#debug=True#debug=False#" "$f"
python3 - "$f" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
s = s.replace("app = Flask(__name__)", "import multiprocessing\nshared = multiprocessing.Array('i', 4)\napp = Flask(__name__)", 1)
open(p, "w").write(s)
PY
ls -ld "$(root web)/dev/shm" 2>&1
