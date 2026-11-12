# One process (master_process off) comes from the converter, in nginx.conf; the patch no longer repeats it.
# The upstream keeps its name (backend), because the dns answers to the names of the compose file.
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
grep -n proxy_pass "$(root proxy)/etc/nginx/conf.d/default.conf"
