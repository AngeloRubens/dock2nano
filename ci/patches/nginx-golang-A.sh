# bisect variant A of nginx-golang
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/bisect-lib.sh"
bash "$(dirname "$0")/nginx-golang.sh"
PROJECT=nginx-golang
setip backend 192.168.33.50
setip proxy 192.168.33.51
cat "$(root proxy)/etc/nginx/conf.d/default.conf"
