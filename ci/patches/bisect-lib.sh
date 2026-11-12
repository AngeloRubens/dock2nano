# bisect helper: a static address for a package of the compose file (the manifest RunConfig is read by ops compose)
setip() {
  python3 - "$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_$1/package.manifest" "$2" <<'PY'
import json, sys
p, ip = sys.argv[1], sys.argv[2]
m = json.load(open(p))
rc = m.setdefault("RunConfig", {})
rc["IPAddress"] = ip
rc["Gateway"] = "192.168.33.1"
rc["NetMask"] = "255.255.255.0"
json.dump(m, open(p, "w"), indent=2)
print("static", p, ip)
PY
}
