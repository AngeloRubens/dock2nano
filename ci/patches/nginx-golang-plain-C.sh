# bisect variant C of nginx-golang-plain
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/bisect-lib.sh"
bash "$(dirname "$0")/nginx-golang-plain.sh"
PROJECT=nginx-golang
cat > "$(root proxy)/etc/nginx/conf.d/default.conf" <<'CONF'
server {
  listen 80;
  resolver 192.168.33.2 valid=1s;
  location / {
    set $up backend.service;
    proxy_pass http://$up:80;
  }
}
CONF
cat "$(root proxy)/etc/nginx/conf.d/default.conf"
