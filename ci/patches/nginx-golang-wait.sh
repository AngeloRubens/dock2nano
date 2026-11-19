# Same changes as nginx-golang.sh, plus the option of nanovms/ops issue 1813: nanos waits for an
# ipv4 address before it starts the program (key exec_wait_for_ip4_secs of ManifestPassthrough).
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
bash "$(dirname "$0")/nginx-golang.sh"
m="$OPS_HOME/.ops/local_packages/amd64/${PROJECT}_proxy/package.manifest"
echo "before:"; head -c 1500 "$m"; echo
tmp=$(mktemp)
jq '.ManifestPassthrough = ((.ManifestPassthrough // {}) + {"exec_wait_for_ip4_secs": "30"})' "$m" > "$tmp" && cat "$tmp" > "$m"
echo "after:"; head -c 1500 "$m"; echo
