# Docker Compose

Example configuration for running an instrumented version of gowebserver via docker-compose.

## Running

```bash
# Install the Grafana Loki Logging Plugin
# https://grafana.com/docs/loki/latest/clients/docker-driver/
docker plugin install grafana/loki-docker-driver:latest --alias loki --grant-all-permissions

# Run the cluster.
docker-compose up -d

# Stop cluster
docker-compose rm -v -s; docker-compose down --remove-orphans -v
```

## Sites

| Application | Endpoint                        |
| ----------- | ------------------------------- |
| Gowebserver | <http://localhost:28080>        |
| Prometheus  | <http://localhost:9090>         |
| Jaeger      | <http://localhost:16686>        |
| Loki        | <http://localhost:3100/metrics> |
| Grafana     | <http://localhost:3000>         |
