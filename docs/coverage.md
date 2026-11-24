# Coverage of docker/awesome-compose

Every sample of docker/awesome-compose that has a compose file with a published or exposed HTTP or TCP port is a row of the matrix of .github/workflows/compose-up.yml. Plain rows (no flags) measure what the tool does without help. Flags appear only where the project notes record a command that was needed (django, fastapi). Results are filled from CI runs of the fork; until then a cell is "not run". The probe is strict: green means the status is 2xx or 3xx, or the TCP port is open. A 4xx or 5xx answer prints "result: SVC reached, status N is not a success" and fails the job.

## Results

| Sample (job) | Flags | Probe | Result | Run id |
| --- | --- | --- | --- | --- |
| django-host | --command 'web=manage.py runserver 0.0.0.0:8000 --noreload' (Host: localhost:8000) | web:8000:http | not run | not run |
| django-ip | --command 'web=manage.py runserver 0.0.0.0:8000 --noreload' | web:8000:http | not run | not run |
| flask | - | web:8000:http | not run | not run |
| fastapi | --command 'api=uvicorn app.main:app --host 0.0.0.0 --port 8000 --workers 1' | api:8000:http | not run | not run |
| angular | - | web:4200:http | not run | not run |
| apache-php | - | web:80:http | not run | not run |
| aspnet-mssql | - | web:80:http | not run | not run |
| elasticsearch-logstash-kibana | - | elasticsearch:9200:http logstash:9600:http kibana:5601:http | not run | not run |
| gitea-postgres | - | gitea:3000:http db:5432:tcp | not run | not run |
| minecraft | - | minecraft:25565:tcp | not run | not run |
| nextcloud-postgres | - | nc:80:http | not run | not run |
| nextcloud-redis-mariadb | - | nc:80:http | not run | not run |
| nginx-aspnet-mysql | - | proxy:80:http | not run | not run |
| nginx-flask-mysql | - | proxy:80:http backend:8000:http | not run | not run |
| nginx-golang-mysql | - | proxy:80:http | not run | not run |
| nginx-golang-postgres | - | proxy:80:http | not run | not run |
| nginx-wsgi-flask | - | nginx-proxy:80:http flask-app:8000:http | not run | not run |
| pihole-cloudflared-DoH | - | pihole:80:http | not run | not run |
| postgresql-pgadmin | - | postgres:5432:tcp pgadmin:80:http | not run | not run |
| prometheus-grafana | - | prometheus:9090:http grafana:3000:http | not run | not run |
| react-express-mysql | - | frontend:3000:http backend:80:http | not run | not run |
| react-java-mysql | - | frontend:3000:http | not run | not run |
| react-nginx | - | frontend:80:http | not run | not run |
| react-rust-postgres | - | frontend:3000:http db:5432:tcp | not run | not run |
| sparkjava | - | sparkjava:8080:http | not run | not run |
| spring-postgres | - | backend:8080:http db:5432:tcp | not run | not run |
| vuejs | - | web:8080:http | not run | not run |
| nginx-golang | - | proxy:80:http backend:80:http | not run | not run |
| nginx-golang-plain | --short-names | proxy:80:http backend:80:http | not run | not run |
| sparkjava-mysql | - | backend:8080:http db:3306:tcp | not run | not run |
| sparkjava-mysql-short | --short-names | backend:8080:http db:3306:tcp | not run | not run |
| sparkjava-mysql-plain | --short-names --with db | backend:8080:http db:3306:tcp | not run | not run |
| flask-redis | - | web:8000:http redis:6379:tcp | not run | not run |
| nginx-nodejs-redis | - | nginx:80:http web1:5000:http redis:6379:tcp | not run | not run |
| react-express-mongodb | - | frontend:3000:http backend:3000:http mongo:27017:tcp | not run | not run |
| nginx-flask-mongo | - | web:80:http backend:9091:http mongo:27017:tcp | not run | not run |
| nginx-flask-mongo-plain | --short-names | web:80:http backend:9091:http mongo:27017:tcp | not run | not run |

## Skipped samples

| Sample | Reason |
| --- | --- |
| official-documentation-samples | no compose file at the top of the directory (subdirectories django, rails, wordpress hold the samples); the wordpress and django ones duplicate rows above |
| plex | network_mode host: the guest has no host network, and the service needs the media paths of the host |
| portainer | mounts the Docker socket of the host (/var/run/docker.sock): it manages a Docker daemon that a unikernel does not have |
| traefik-golang | traefik reads the Docker socket (--providers.docker) to find backends and routes by container labels: nothing to probe without it |
| wireguard | needs NET_ADMIN, kernel modules and a UDP port: not an HTTP or TCP probe |
| wasmedge-kafka-mysql | images run on a WasmEdge runtime (wasm platform), not a Linux program |
| wasmedge-mysql-nginx | the backend image is a wasm module run by WasmEdge, not a Linux program |

## Probed in part

- pihole-cloudflared-DoH: only the web port 80 of pihole is probed; the DNS ports (53 tcp and udp, 67 udp) and the cloudflared service are not probed. The compose file needs the variables TIMEZONE and PIHOLE_PW, which are not set in CI, and a fixed address in a network.
- elasticsearch-logstash-kibana, postgresql-pgadmin, aspnet-mssql: database ports are probed only where listed in the Probe column.
- Services of a row that are not in its probe list start with the rest but are not asked.
