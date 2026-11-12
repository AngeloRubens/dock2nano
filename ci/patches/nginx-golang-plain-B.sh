# bisect variant B of nginx-golang-plain
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/bisect-lib.sh"
bash "$(dirname "$0")/nginx-golang-plain.sh"
PROJECT=nginx-golang
setip backend 192.168.33.50
setip proxy 192.168.33.51
sed -i 's#http://backend[.a-z]*:80#http://192.168.33.50:80#' "$(root proxy)/etc/nginx/conf.d/default.conf"
cat "$(root proxy)/etc/nginx/conf.d/default.conf"
