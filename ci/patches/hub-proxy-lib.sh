# test only: the proxy is the nginx package of the hub (eyberg/nginx:1.28.3), with the default.conf the converter packaged
# usage: hubproxy <proxy_pass target>   (PROJECT is set)
hubproxy() {
  target="$1"
  ops pkg get eyberg/nginx:1.28.3 2>&1 | tail -2
  hubdir="$OPS_HOME/.ops/packages/amd64/eyberg/nginx_1.28.3"
  ours="$(root proxy)/etc/nginx/conf.d/default.conf"
  echo "--- default.conf of the converter's package"; cat "$ours"; echo
  mkdir -p "$hubdir/sysroot/etc/nginx/conf.d"
  sed "s#http://backend[.a-z]*:80#$target#" "$ours" > "$hubdir/sysroot/etc/nginx/conf.d/default.conf"
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
  # ops compose decodes every manifest into one shared config, so a field the hub manifest lacks keeps the value
  # of the package before it (the backend's cwd /code): say the cwd of the hub package
  python3 - "$hubdir/package.manifest" <<'PY'
import json, sys
p = sys.argv[1]
m = json.load(open(p))
m["ManifestPassthrough"] = {"cwd": "/"}
json.dump(m, open(p, "w"), indent=2)
PY
  echo "--- hub package as used"; cat "$hubdir/package.manifest"; cat "$hubdir/sysroot/etc/nginx/nginx.conf" "$hubdir/sysroot/etc/nginx/conf.d/default.conf"
  echo "--- converter's proxy manifest"; head -c 1500 "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_proxy/package.manifest"; echo
  python3 - /tmp/out/compose.yaml <<'PY'
import re, sys
p = sys.argv[1]
s = open(p).read()
n = re.sub(r"(  - pkg: proxy\n)    name: \S+\n    local: true\n", r"\1    name: eyberg/nginx:1.28.3\n    local: false\n", s)
assert n != s, "the proxy package was not found in compose.yaml"
open(p, "w").write(n)
print(n[n.index("packages:"):])
PY
}
