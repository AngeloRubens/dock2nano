# nginx forks a master and workers: one process. The upstream keeps its name (backend), because
# the dns answers to the names of the compose file.
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_proxy/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["Args"] = ["nginx", "-g", "daemon off; master_process off;"]
json.dump(m, open(p, "w"), indent=2)
PY
grep -n proxy_pass "$(root proxy)/etc/nginx/conf.d/default.conf"
