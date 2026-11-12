# bisect variant B of nginx-flask-mongo
PROJECT=nginx-flask-mongo
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/bisect-lib.sh"
bash "$(dirname "$0")/nginx-flask-mongo.sh"
PROJECT=nginx-flask-mongo
setip backend 192.168.33.50
setip web 192.168.33.51
setip mongo 192.168.33.52
sed -i 's#http://backend[.a-z]*:9091#http://192.168.33.50:9091#' "$(root web)/etc/nginx/conf.d/default.conf"
cat "$(root web)/etc/nginx/conf.d/default.conf"
