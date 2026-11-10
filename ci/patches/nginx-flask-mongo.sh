PROJECT=nginx-flask-mongo
. "$(dirname "$0")/lib.sh"
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_web/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
print("web args:", m["Args"])
PY
r="$(root backend)"
sed -i 's#MongoClient("mongo:27017")#MongoClient("mongo.service:27017")#; s#debug=True#debug=False#' "$r/src/server.py"
grep -n "MongoClient\|debug" "$r/src/server.py"
c="$(root web)/etc/nginx/conf.d/default.conf"
cat "$c"
sed -i 's#http://backend:9091#http://backend.service:9091#' "$c"
grep -n proxy_pass "$c"
