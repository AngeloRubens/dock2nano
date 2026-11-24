# Coverage of docker/awesome-compose

Every sample of docker/awesome-compose that has a compose file with a published or exposed HTTP or TCP port is a row of the matrix of .github/workflows/compose-up.yml. Plain rows (no flags) measure what the tool does without help. Flags appear only where the project notes record a command that was needed (django, fastapi). Results are filled from CI runs of the fork; until then a cell is "not run". The probe is strict: green means the status is 2xx or 3xx, or the TCP port is open. A 4xx or 5xx answer prints "result: SVC reached, status N is not a success" and fails the job.

## Results

| Sample (job) | Flags | Probe | Result | Run id |
| --- | --- | --- | --- | --- |
| django-host | --command 'web=manage.py runserver 0.0.0.0:8000 --noreload' (Host: localhost:8000) | web:8000:http | red: reached, status 500 | 45372721961 |
| django-ip | --command 'web=manage.py runserver 0.0.0.0:8000 --noreload' | web:8000:http | red: reached, status 400 (DisallowedHost) | 45372721961 |
| flask | - | web:8000:http | green | 45372721961 |
| fastapi | --command 'api=uvicorn app.main:app --host 0.0.0.0 --port 8000 --workers 1' | api:8000:http | green | 45372721961 |
| angular | - | web:4200:http | red: no answer (000) | 45372721961 |
| apache-php | - | web:80:http | red: no answer (000) | 45372721961 |
| aspnet-mssql | - | web:80:http | red: no address | 45372721961 |
| elasticsearch-logstash-kibana | - | elasticsearch:9200:http logstash:9600:http kibana:5601:http | red: no address (names of services in config files noted) | 45372721961 |
| gitea-postgres | - | gitea:3000:http db:5432:tcp | red: gitea no answer, db port open | 45372721961 |
| minecraft | - | minecraft:25565:tcp | red: no address | 45372721961 |
| nextcloud-postgres | - | nc:80:http | red: no answer | 45372721961 |
| nextcloud-redis-mariadb | - | nc:80:http | red: no answer | 45372721961 |
| nginx-aspnet-mysql | - | proxy:80:http | red: proxy no answer | 45372721961 |
| nginx-flask-mysql | - | proxy:80:http backend:8000:http | red: no answer; observation of backend: ImportError url_quote from werkzeug.urls | 45372721961 |
| nginx-golang-mysql | - | proxy:80:http | red: proxy no answer | 45372721961 |
| nginx-golang-postgres | - | proxy:80:http | red: proxy no answer | 45372721961 |
| nginx-wsgi-flask | - | nginx-proxy:80:http flask-app:8000:http | red: no address; docker build failed at pip install -r requirements.txt | 45372721961 |
| pihole-cloudflared-DoH | - | pihole:80:http | red: no answer | 45372721961 |
| postgresql-pgadmin | - | postgres:5432:tcp pgadmin:80:http | red: no address; the postgres container stopped with status 1 during the observation | 45372721961 |
| prometheus-grafana | - | prometheus:9090:http grafana:3000:http | red: no answer, no address | 45372721961 |
| react-express-mysql | - | frontend:3000:http backend:80:http | red: no address, backend no answer | 45372721961 |
| react-java-mysql | - | frontend:3000:http | red: no address | 45372721961 |
| react-nginx | - | frontend:80:http | green | 45372721961 |
| react-rust-postgres | - | frontend:3000:http db:5432:tcp | red: no address; docker build of backend (target development) failed | 45372721961 |
| sparkjava | - | sparkjava:8080:http | green | 45372721961 |
| spring-postgres | - | backend:8080:http db:5432:tcp | red: backend no answer, db open | 45372721961 |
| vuejs | - | web:8080:http | red: no address; docker build (target development) failed | 45372721961 |
| nginx-golang | - | proxy:80:http backend:80:http | green | 45372721961 |
| nginx-golang-plain | --short-names | proxy:80:http backend:80:http | green | 45372721961 |
| sparkjava-mysql | - | backend:8080:http db:3306:tcp | red: backend no answer, db open | 45372721961 |
| sparkjava-mysql-short | --short-names | backend:8080:http db:3306:tcp | green | 45372721961 |
| sparkjava-mysql-plain | --short-names --with db | backend:8080:http db:3306:tcp | green | 45372721961 |
| flask-redis | - | web:8000:http redis:6379:tcp | green | 45372721961 |
| nginx-nodejs-redis | - | nginx:80:http web1:5000:http redis:6379:tcp | green | 45372721961 |
| react-express-mongodb | - | frontend:3000:http backend:3000:http mongo:27017:tcp | red: frontend and backend no answer, mongo open | 45372721961 |
| nginx-flask-mongo | - | web:80:http backend:9091:http mongo:27017:tcp | green | 45372721961 |
| nginx-flask-mongo-plain | --short-names | web:80:http backend:9091:http mongo:27017:tcp | green | 45372721961 |

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

Reading of run 45372721961 (2026-11-24, ops master, nightly nanos): 13 of 37 jobs green, 24 red. The causes in the table are the first visible symptom in the log, not a diagnosis: the rows that say "no answer" or "no address" were not yet investigated one by one, and each needs its own look before a fix is claimed. The probe asks "/" with Host ip:port, so a service that rejects that Host (django-ip) counts as red under the strict probe.
