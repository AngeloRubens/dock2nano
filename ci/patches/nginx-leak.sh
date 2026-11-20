# nanovms/ops issue 1814: a package inherits fields of the package started before it.
# compose.yaml starts backend first, then proxy (depends_on). The backend gets a cwd that exists
# only in its own sysroot, the proxy gets no cwd at all (as a hub package has none). An ops that
# keeps the config of the previous package starts the proxy with the cwd of the backend and nginx
# fails with "unable to change cwd"; an ops that gives each package a new config starts it.
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
bash "$(dirname "$0")/nginx-golang-wait.sh"
B="$(root backend)"; P="$(root proxy)"
d=$(cd "$B" && find . -mindepth 1 -maxdepth 2 -type d | sed 's#^\./#/#' | while read -r x; do [ -e "$P$x" ] || { echo "$x"; break; }; done)
echo "dir in the backend sysroot and not in the proxy sysroot: ${d:-NONE}"
[ -n "$d" ] || { echo "no such directory"; exit 1; }
mb="$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_backend/package.manifest"
mp="$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_proxy/package.manifest"
t=$(mktemp); jq --arg d "$d" '.ManifestPassthrough.cwd = $d' "$mb" > "$t" && cat "$t" > "$mb"
t=$(mktemp); jq 'del(.ManifestPassthrough.cwd)' "$mp" > "$t" && cat "$t" > "$mp"
echo "backend manifest:"; head -c 800 "$mb"; echo; echo "proxy manifest:"; head -c 800 "$mp"; echo
echo "order in compose.yaml:"; grep -n "name:\|- name\|pkg:" /tmp/out/compose.yaml | head -12
