# nginx forks a master and workers: one process, as in the nginx package of the ops repository.
# The upstream is backend:80; on ops a service is backend.service.
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_proxy/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["Args"] = ["nginx", "-g", "daemon off; master_process off;"]
json.dump(m, open(p, "w"), indent=2)
PY
sed -i 's#http://backend:80#http://backend.service:80#' "$(root proxy)/etc/nginx/conf.d/default.conf"
grep -n proxy_pass "$(root proxy)/etc/nginx/conf.d/default.conf"
