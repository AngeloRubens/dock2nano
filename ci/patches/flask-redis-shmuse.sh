# Variant C: what the empty directory is for. The app is replaced by one that uses
# multiprocessing the way the traceback did (a shared array), with the directory and without it.
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
grep -n "multiprocessing" "$f"
