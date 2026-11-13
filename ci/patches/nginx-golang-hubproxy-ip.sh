# hub nginx as the proxy, target http://IPOF_backend:80
PROJECT=nginx-golang
. "$(dirname "$0")/lib.sh"
. "$(dirname "$0")/hub-proxy-lib.sh"
bash "$(dirname "$0")/nginx-golang.sh"
hubproxy 'http://IPOF_backend:80'
