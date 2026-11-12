# bisect variant C of nginx-flask-mongo-plain
PROJECT=nginx-flask-mongo
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/bisect-lib.sh"
bash "$(dirname "$0")/nginx-flask-mongo-plain.sh"
PROJECT=nginx-flask-mongo
cat > "$(root web)/etc/nginx/conf.d/default.conf" <<'CONF'
server {
  listen 80;
  resolver 192.168.33.2 valid=1s;
  location / {
    set $up backend.service;
    proxy_pass http://$up:9091;
  }
}
CONF
cat "$(root web)/etc/nginx/conf.d/default.conf"
