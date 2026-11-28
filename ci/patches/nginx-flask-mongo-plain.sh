# Flask without the reloader; the names stay as the compose file has them.
PROJECT=nginx-flask-mongo
. "$(dirname "$0")/lib.sh"
r="$(root backend)"
sed -i 's#debug=True#debug=False#' "$r/src/server.py"
grep -n "MongoClient\|debug" "$r/src/server.py"
grep -n proxy_pass "$(root web)/etc/nginx/conf.d/default.conf"
