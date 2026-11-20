# test only: the front end "nginx" of nginx-nodejs-redis is the nginx package of the hub
# (eyberg/nginx:1.28.3), with the default.conf the converter packaged (after the same hand changes as
# nginx-nodejs-redis.sh: web1.service and web2.service), and the same address wait.
PROJECT=nginx-nodejs-redis
. "$(dirname "$0")/lib.sh"
bash "$(dirname "$0")/nginx-nodejs-redis.sh"
ops pkg get eyberg/nginx:1.28.3 2>&1 | tail -2
hubdir="$OPS_HOME/.ops/packages/amd64/eyberg/nginx_1.28.3"
ours="$(root nginx)/etc/nginx/conf.d/default.conf"
echo "--- default.conf of the converter's package"; cat "$ours"; echo
mkdir -p "$hubdir/sysroot/etc/nginx/conf.d"
cp "$ours" "$hubdir/sysroot/etc/nginx/conf.d/default.conf"
cat > "$hubdir/sysroot/etc/nginx/nginx.conf" <<'CONF'
worker_processes 1;
daemon off;
master_process off;

pid bob.pid;

error_log stderr notice;

events {
    worker_connections 1024;
}

http {
    access_log off;
    include /etc/nginx/conf.d/*.conf;
}
CONF
cp "$hubdir/sysroot/etc/nginx/nginx.conf" "$hubdir/sysroot/usr/local/nginx/conf/nginx.conf"
m="$hubdir/package.manifest"
t=$(mktemp); jq '.ManifestPassthrough = ((.ManifestPassthrough // {}) + {"exec_wait_for_ip4_secs": "30"})' "$m" > "$t" && cat "$t" > "$m"
echo "--- hub package as used"; cat "$m"; cat "$hubdir/sysroot/etc/nginx/nginx.conf" "$hubdir/sysroot/etc/nginx/conf.d/default.conf"
echo "--- converter's nginx manifest"; head -c 1500 "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_nginx/package.manifest"; echo
python3 - /tmp/out/compose.yaml <<'PY'
import re, sys
p = sys.argv[1]
s = open(p).read()
n = re.sub(r"(  - pkg: nginx\n)    name: \S+\n    local: true\n", r"\1    name: eyberg/nginx:1.28.3\n    local: false\n", s)
assert n != s, "the nginx package was not found in compose.yaml"
open(p, "w").write(n)
print(n[n.index("packages:"):])
PY
