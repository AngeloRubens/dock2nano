# bisect variant B of nginx-flask-mongo
PROJECT=nginx-flask-mongo
. "$(dirname "$0")/lib.sh"
bash "$(dirname "$0")/nginx-flask-mongo.sh"
PROJECT=nginx-flask-mongo
sed -i 's#http://backend[.a-z]*:9091#http://IPOF_backend:9091#' "$(root web)/etc/nginx/conf.d/default.conf"
cat "$(root web)/etc/nginx/conf.d/default.conf"
