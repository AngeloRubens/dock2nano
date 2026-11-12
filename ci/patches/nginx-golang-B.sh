# bisect variant B of nginx-golang
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
bash "$(dirname "$0")/nginx-golang.sh"
PROJECT=nginx-golang
sed -i 's#http://backend[.a-z]*:80#http://IPOF_backend:80#' "$(root proxy)/etc/nginx/conf.d/default.conf"
cat "$(root proxy)/etc/nginx/conf.d/default.conf"
