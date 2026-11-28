# Variant B: no /dev/shm directory, the shmem klib of nanos in the package.
PROJECT=flask-redis
. "$(dirname "$0")/lib.sh"
f="$(root web)/code/app.py"
sed -i "s#host='redis'#host='redis.service'#; s#debug=True#debug=False#" "$f"
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_web/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["Klibs"] = ["shmem"]
json.dump(m, open(p, "w"), indent=2)
PY
ls "$OPS_HOME"/.ops/*/klibs 2>/dev/null | head -30
