# Grafana dashboards

Dashboards for a gowebserver scraped by Prometheus (see [../prometheus](../prometheus/README.md)).

Dashboard | File | Shows
----------|------|------
gowebserver | [dashboards/gowebserver.json](dashboards/gowebserver.json) | Requests by what answered them, time to first byte, directory listings by stage, the text viewer, uploads, proxied requests, and the process (CPU, memory, goroutines, file descriptors).
gowebserver thumbnails | [dashboards/gowebserver-thumbnails.json](dashboards/gowebserver-thumbnails.json) | Thumbnail requests and how many come from the store, each stage of making one, the render slots, the store's size and speed, and ffmpeg.

Each has a data source, a job and an instance picker at the top. The job defaults to `gowebserver`, the name the example scrape configuration uses.

## Installing

Import the JSON files in Grafana (Dashboards, New, Import), or provision them:

```bash
docker run -p 3000:3000 \
  -v "$PWD/dashboards:/var/lib/grafana/dashboards/gowebserver:ro" \
  -v "$PWD/provisioning/dashboards:/etc/grafana/provisioning/dashboards:ro" \
  -v "$PWD/provisioning/datasources:/etc/grafana/provisioning/datasources:ro" \
  grafana/grafana
```

[provisioning/datasources/prometheus.yaml](provisioning/datasources/prometheus.yaml) points at `http://prometheus:9090`; change it to where Prometheus is. [../compose](../compose/README.md) runs the whole set together.

## Reading them

- **Handler** is what answered a request: `file`, `listing`, `thumbnail`, `videometa` (a video's metadata, preview sprite or subtitles as WebVTT), `mediainfo` (a photo's, video's or song's details for the slideshow's info panel, and the world map it draws a location on), `richview`, `upload`, `proxy` or `index`. Everything under a served path has one route, so this is what tells a directory listing from a video being watched.
- **Time to first byte** is the server's own work. Request duration also has the time the client took to download the body, which for a large file says nothing about the server.
- **Stages** split one kind of work into its steps, each a histogram labeled `stage`. The same steps are the spans of a request's trace (`listing.read`, `thumbnail.decode`, `ffmpeg.frame` and so on), so a slow stage on the dashboard can be looked up as a slow span.
- Panels that name an alert show the number that alert watches.
