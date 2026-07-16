# Viddown

Personal page / HLS downloader on zenbook-server. Headless Chromium probe → pick video + quality → ffmpeg → Filebrowser **`maxi1508`**.

| Item | Value |
|------|--------|
| URL | https://viddown.maximillianleonard.dev |
| Local debug | http://127.0.0.1:8091 |
| Stack | `/srv/apps/viddown/` |
| Image | Local build (`Dockerfile`: Vite + Go + Chromium + ffmpeg) |
| Output | Host `…/files/users/maxi1508/Downloads/videos` → `/data/output` |
| Tunables | `./.env` (mode `600`) — template `.env.example` |
| Auth | None (Tailscale perimeter) |
| Full rebuild | `~/SETUP.md` §16a |
| Day-2 | `~/GUIDE-AND-VARIABLES.md` §5.10 + §7.1 |

## Start / update

```bash
cd /srv/apps/viddown
# edit .env if needed; chmod 600 .env
docker compose up -d --build
docker compose logs -f --tail=50
curl -sS http://127.0.0.1:8091/api/health
```

## Output

Files land in Filebrowser → **My Files** → `Downloads/videos/`. No Filebrowser API — compose bind-mounts that folder.

```bash
ls -lh /srv/apps/filebrowser-quantum/files/users/maxi1508/Downloads/videos/
```

Keep downloads here (not Immich library roots) unless you want them scanned into `gallery.`.

## API (local)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/health` | ffmpeg / output readiness |
| POST | `/api/probe` | `{ "url": "…" }` → `{ "id" }` |
| GET | `/api/probe/:id` | status + `videos[].qualities[]` |
| POST | `/api/download` | `{ probeId, videoId, qualityId }` |
| GET | `/api/download/:id` | progress + Filebrowser-relative path |

One probe and one download at a time. Probe session (headers) ~30 minutes.

## Hard sites / Chromium

Web UI is **headless auto** only. Pages that need manual play / login → use the local CLI (`--visible`).

Container runs Chromium as uid `1000` with `HOME=/home/viddown` and a fresh `--user-data-dir` per probe (avoids crashpad / SingletonLock failures). Compose uses `init: true` so orphaned Chrome children are reaped.

## Traefik

Router `viddown` on Docker network `proxy`. Host publish is loopback-only (`127.0.0.1:8091`).

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

- App source + `.env`: `/srv/apps/viddown/`
- Downloaded media: already under Filebrowser `maxi1508` (Phase 8 restic)
