PROJECT=nginx-nodejs-redis
. "$(dirname "$0")/lib.sh"
python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_nginx/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["Args"] = ["nginx", "-g", "daemon off;"]
json.dump(m, open(p, "w"), indent=2)
PY
c="$(root nginx)/etc/nginx/conf.d/default.conf"
sed -i 's#server web1:5000#server web1.service:5000#; s#server web2:5000#server web2.service:5000#' "$c"
grep -n server "$c" || true
for w in web1 web2; do
  f="$(root $w)/usr/src/app/server.js"
  [ -f "$f" ] && sed -i "s#host: 'redis'#host: 'redis.service'#" "$f" && grep -n "host:" "$f" || true
done
exit 0
