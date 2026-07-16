# Viddown

Personal page / HLS downloader. Headless Chromium probe → pick video + quality → ffmpeg → save to a host output folder.

| Item | Value |
|------|--------|
| URL | `https://viddown.example.com` (via Traefik) |
| Local debug | `http://127.0.0.1:8091` |
| Stack | Docker Compose in project root |
| Image | Local build (`Dockerfile`: Vite + Go + Chromium + ffmpeg) |
| Output | Host download folder → `/data/output` in container |
| Tunables | `.env` (mode `600`) — template `.env.example` |
| Auth | None (rely on network perimeter, e.g. Tailscale) |

## Start / update

```bash
# from project root
# edit .env if needed; chmod 600 .env
docker compose up -d --build
docker compose logs -f --tail=50
curl -sS http://127.0.0.1:8091/api/health
```

## Output

Files are written to the host folder set in `HOST_OUTPUT_DIR` (bind-mounted as `/data/output`). Point this at wherever you want finished videos to land — e.g. a Filebrowser user folder or any directory you browse regularly.

```bash
ls -lh "$HOST_OUTPUT_DIR"
```

## API (local)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/health` | ffmpeg / output readiness |
| POST | `/api/probe` | `{ "url": "…" }` → `{ "id" }` |
| GET | `/api/probe/:id` | status + `videos[].qualities[]` |
| POST | `/api/download` | `{ probeId, videoId, qualityId }` |
| GET | `/api/download/:id` | progress + output-relative path |

One probe and one download at a time. Probe session (headers) ~30 minutes.

## Hard sites / Chromium

Web UI is **headless auto** only. Pages that need manual play / login → use the local CLI (`--visible`).

Container runs Chromium as uid `1000` with `HOME=/home/viddown` and a fresh `--user-data-dir` per probe (avoids crashpad / SingletonLock failures). Compose uses `init: true` so orphaned Chrome children are reaped.

## Traefik

Router `viddown` on external Docker network `proxy`. Host publish is loopback-only (`127.0.0.1:8091`).

## Env

Copy `.env.example` → `.env` and set at least `HOST_OUTPUT_DIR` to your download folder on the host.

| Variable | Example | Notes |
|----------|---------|--------|
| `HOST_OUTPUT_DIR` | `/path/to/Downloads/videos` | **Host** folder bind-mounted for saves |
| `OUTPUT_DIR` | `/data/output` | Path **inside** the container (must match app writes) |
| `HOST_IP` / `HOST_PORT` | `127.0.0.1` / `8091` | Published bind |
| `CONTAINER_PORT` / `LISTEN_ADDR` | `8091` / `:8091` | App listen + Traefik service port |
| `PUID` / `PGID` | `1000` | Container user (own the host folder) |
| `PROBE_TIMEOUT` | `45s` | Chromium probe deadline |
| `CHROME_PATH` | `/usr/bin/chromium` | Debian package path |
| `TRAEFIK_HOST` | `viddown.example.com` | Public hostname |
| `TRAEFIK_NETWORK` | `proxy` | External Docker network name |
| `TZ` / `MEMORY_LIMIT` | … | Locale / cgroup limit |

## Backup

- App source + `.env`: project directory on the host
- Downloaded media: whatever path you set in `HOST_OUTPUT_DIR`
