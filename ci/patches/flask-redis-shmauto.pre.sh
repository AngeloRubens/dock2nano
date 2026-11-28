# Before the conversion: the source of the app uses multiprocessing, so that the observation sees
# the program name /dev/shm. debug is off because the reloader forks.
f=/tmp/ac/flask-redis/app.py
sed -i "s#debug=True#debug=False#" "$f"
python3 - "$f" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
s = s.replace("app = Flask(__name__)", "import multiprocessing\nshared = multiprocessing.Array('i', 4)\napp = Flask(__name__)", 1)
open(p, "w").write(s)
PY
grep -n "multiprocessing\|debug" "$f"
