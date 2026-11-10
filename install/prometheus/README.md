# Prometheus configuration

Scraping and alerts for gowebserver. The dashboards that go with them are in [../grafana](../grafana/README.md).

File | What it is
-----|-----------
[prometheus.yaml](prometheus.yaml) | An example configuration: scrapes `gowebserver:8080/metrics` as the job `gowebserver` and loads the rules.
[rules/gowebserver-alerts.yaml](rules/gowebserver-alerts.yaml) | The alerts.
[tests/gowebserver-alerts.test.yaml](tests/gowebserver-alerts.test.yaml) | Unit tests for the alerts.

gowebserver serves its metrics at `-monitoring.metrics.path` (`/metrics` unless changed) on its HTTP port.

## Using the alerts

Add the rules file to `rule_files` in your own configuration. The rules select `job="gowebserver"`: scrape under that job name, or change it in the file.

```bash
promtool check config prometheus.yaml
promtool test rules tests/gowebserver-alerts.test.yaml
```

## Alerts

Alert | Fires when | Severity
------|-----------|---------
GowebserverDown | The server can't be scraped for 2 minutes. | critical
GowebserverRestarting | It restarted more than twice in 30 minutes. | warning
GowebserverServerErrors | More than 5% of requests got a 5xx response for 10 minutes. | warning
GowebserverSlowResponses | For a handler other than thumbnail, upload and proxy, the slowest 5% of requests waited more than 1 second for the response to start, for 10 minutes. | warning
GowebserverSlowProxyUpstream | The slowest 5% of proxied requests took more than 5 seconds, for 10 minutes. | warning
GowebserverProxyUpstreamErrors | More than 10% of proxied requests failed or got a 5xx response for 10 minutes. | warning
GowebserverThumbnailBacklog | The slowest 5% of thumbnails waited more than 5 seconds for a render slot, for 10 minutes. | warning
GowebserverThumbnailFailures | More than 10% of photo or video thumbnail requests failed for 15 minutes. | warning
GowebserverFFmpegTimeouts | More than 3 runs of ffmpeg were stopped at their 30 second limit in 15 minutes. | warning
GowebserverSlowThumbnailStore | The slowest 5% of thumbnail store lookups took more than 250 ms, for 10 minutes. | warning
GowebserverThumbnailStoreLarge | The thumbnail database is over 20 GiB. | warning
GowebserverUploadFailures | An upload could not be stored in the last 15 minutes. | warning
GowebserverHighMemory | The process holds more than 80% of the machine's memory for 10 minutes. | warning
GowebserverFileDescriptors | More than 80% of its file descriptors are open for 10 minutes. | warning
GowebserverGoroutines | More than 10000 goroutines for 15 minutes. | warning

The thresholds are starting points. The ones most worth setting for your own server:

- **GowebserverThumbnailStoreLarge**: the store never evicts, so set the size to what its disk can spare.
- **GowebserverSlowResponses**: a folder of tens of thousands of files on a slow disk can take more than a second to list. Raise the threshold if that is normal for you.
- **GowebserverHighMemory**: compares with the memory of the machine as the server sees it. In a container with a memory limit, compare with the limit instead.

## Metrics

Besides the Go runtime, process and host metrics:

Metric | Labels | What it measures
-------|--------|-----------------
`http_server_request_duration_seconds` | `handler`, `http_response_status_code`, `http_request_method`, `http_route` | Requests, sending the body included.
`http_server_time_to_first_byte_seconds` | `handler` | From a request arriving to its response starting.
`http_server_active_requests` | `handler` | Requests being served now.
`http_server_response_body_size_bytes` | `handler`, ... | Response sizes.
`http_client_request_duration_seconds` | `server_address`, `http_response_status_code` | Requests the reverse proxy passed on.
`listing_stage_duration_seconds` | `stage` (open, read, stat, sort, render) | Each stage of listing a directory.
`listing_entries` | | Entries in the directories listed.
`richview_requests_total` | `result` | Text viewer requests.
`richview_stage_duration_seconds` | `stage` (read, highlight, render) | Each stage of showing a file in the text viewer.
`richview_source_bytes` | | Size of the files highlighted.
`richview_follow_connections_total` | `result` (ended, gone, limit, refused, failed) | Streams that follow a log while it is written, by how each ended.
`richview_follow_active` | | Logs being streamed to browsers right now.
`richview_follow_lines_total` | | Lines of followed logs streamed to browsers.
`upload_request_duration_seconds` | `result` (ok, rejected, failed) | Upload requests.
`uploaded_bytes_total`, `uploaded_files_total` | | What was uploaded.
`thumbnail_requests_total`, `thumbnail_request_duration_seconds` | `kind` (photo, video), `result` | Thumbnail requests.
`thumbnail_stage_duration_seconds` | `kind`, `stage` (wait, decode, scale, encode, store) | Each stage of making a thumbnail.
`thumbnail_renders` | `kind`, `state` (waiting, running) | Thumbnails being made now.
`thumbnail_render_slots` | | How many can be made at once.
`thumbnail_abandoned_total` | | Thumbnails every request left while they were being made.
`thumbnail_source_megapixels` | `kind` | Size of the pictures decoded.
`thumbnail_store_lookup_duration_seconds` | `result` (hit, miss) | Asking the store for a thumbnail.
`thumbnail_stored_bytes_total` | `kind` | Bytes of thumbnails put in the store.
`thumbnail_store_size_bytes` | | Size of the store's database file.
`thumbnail_ffmpeg_duration_seconds` | `operation` (frame, duration, photo, encode), `input` (file, pipe), `outcome` | Each run of ffmpeg: a video's frame, its length, a photo, or a thumbnail made in Go written as WebP. A frame or photo run can make the whole thumbnail, which is then all of its decode stage.
`thumbnail_video_copy_bytes_total` | | Videos copied out of archives for ffmpeg.
