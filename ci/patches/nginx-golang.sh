# One process (master_process off) comes from the converter, in nginx.conf; the patch no longer repeats it.
# The upstream is backend:80; on ops a service is backend.service.
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
sed -i 's#http://backend:80#http://backend.service:80#' "$(root proxy)/etc/nginx/conf.d/default.conf"
grep -n proxy_pass "$(root proxy)/etc/nginx/conf.d/default.conf"
