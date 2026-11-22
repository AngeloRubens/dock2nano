PROJECT=fastapi
. "$(dirname "$0")/lib.sh"
# gunicorn forks its workers and nanos has no fork: run uvicorn in one process.
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_api/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["Args"] = ["/usr/local/bin/python3.9", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
json.dump(m, open(p, "w"), indent=2)
PY
grep -A9 '"Args"' "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_api/package.manifest"
