# Webtoons Downloader (wtd)

[![CI](https://github.com/underhax/go-webtoons-downloader/actions/workflows/ci.yml/badge.svg)](https://github.com/underhax/go-webtoons-downloader/actions/workflows/ci.yml)
[![Release](https://github.com/underhax/go-webtoons-downloader/actions/workflows/release.yml/badge.svg)](https://github.com/underhax/go-webtoons-downloader/actions/workflows/release.yml)
[![GitHub last commit](https://img.shields.io/github/last-commit/underhax/go-webtoons-downloader)](https://github.com/underhax/go-webtoons-downloader/commits/main)
[![GitHub issues](https://img.shields.io/github/issues/underhax/go-webtoons-downloader)](https://github.com/underhax/go-webtoons-downloader/issues)
[![GitHub repo size](https://img.shields.io/github/repo-size/underhax/go-webtoons-downloader)](https://github.com/underhax/go-webtoons-downloader)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A command-line utility and series synchronizer for downloading comics from [WEBTOON](https://www.webtoons.com) into PDF, CBZ, ZIP archives, or raw image directories with embedded metadata (`ComicInfo.xml` and native PDF document properties).

## Key Features

- **Multi-format output:** Direct export to PDF, CBZ, ZIP, or raw image folders.
- **Embedded metadata:** Embeds title, author, genre, summary, and series details into `ComicInfo.xml` (for CBZ, ZIP, and raw images) and native document metadata (for PDF).
- **Delta synchronization:** Skips existing chapters and downloads only new releases.
- **Concurrent downloads:** Worker pool with configurable concurrency limits and per-request rate-limiting delays.
- **Zero dependencies:** Standalone static binary with no external runtimes required.
- **Self-update:** In-place binary self-update (`--update`) from GitHub Releases.
- **Hardened container stack:** Pre-built `scratch` image and Docker Compose configuration with minimal attack surface, strict isolation, and least-privilege execution.

---

<details>
<summary><strong>Installation</strong></summary>

## Installation

Official pre-built standalone binaries are available on the [GitHub Releases page](https://github.com/underhax/go-webtoons-downloader/releases).

1. Download the archive for your operating system and architecture (`.tar.gz` for Linux/macOS, `.zip` for Windows).
2. Unpack the archive into any directory, or place the binary into your `PATH` (e.g., `/usr/local/bin` on Linux/macOS).
3. Run `wtd` from your terminal or command prompt.

</details>

---

<details>
<summary><strong>Containerized Deployment</strong></summary>

## Containerized Deployment (Docker Compose)

**Container Security Architecture:**

* Scratch base image: Contains only the static binary, root CA certificates, and mount points—no shell, package manager, or system utilities.
* Non-root execution: Runs strictly under a dedicated unprivileged user and group with no root fallback.
* Read-only rootfs: Enforces an immutable root filesystem with a volatile tmpfs mount for temporary buffers.
* Privilege restriction: Drops all Linux capabilities and prevents privilege escalation.
* IPC isolation: Private IPC namespace with disabled shared memory.
* Network containment: Isolated bridge network with disabled inter-container communication, restricting egress strictly to external HTTPS endpoints.
* Resource limits: Enforces CPU quotas, memory reservations, and process limits to prevent host resource starvation (can be adjusted in `docker-compose.yaml` if downloading exceptionally large releases).

### 1. Dedicated System Account

Select an unused numeric ID on your host:

```bash
APP_UID=<CHOSEN_UID>

getent passwd ${APP_UID}
getent group ${APP_UID}
```

Create a dedicated system user and group without shell access:

```bash
sudo useradd -m -d /home/wtd -s /usr/sbin/nologin -u ${APP_UID} -U wtd
```

### 2. Configure Permissions & Deployment Files

Create the deployment directory and fetch configuration files:

```bash
sudo mkdir -p /home/wtd
sudo chown root:wtd /home/wtd
sudo chmod 0750 /home/wtd
```

```bash
REPO="https://raw.githubusercontent.com/underhax/go-webtoons-downloader/main"

sudo curl -fsSL ${REPO}/docker-compose.yaml -o /home/wtd/docker-compose.yaml
sudo curl -fsSL ${REPO}/.env.example -o /home/wtd/.env
sudo chmod 0600 /home/wtd/{docker-compose.yaml,.env}
sudo chown root:root /home/wtd/{docker-compose.yaml,.env}
```

Configure host IDs:

```bash
sed -i "s/^APP_UID=.*/APP_UID=${APP_UID}/" /home/wtd/.env
sed -i "s/^APP_GID=.*/APP_GID=${APP_UID}/" /home/wtd/.env
```

Initialize the download directory and set permissions:

```bash
mkdir -p /home/wtd/data/webtoons
sudo chown -R ${APP_UID}:${APP_UID} /home/wtd/data/webtoons
sudo chmod 0700 /home/wtd/data/webtoons
```

### 3. Optional: Batch Synchronization via Container

To synchronize a persistent series list in Docker, create `links.txt`:

```bash
sudo touch /home/wtd/links.txt
sudo chown root:${APP_UID} /home/wtd/links.txt
sudo chmod 0640 /home/wtd/links.txt
```

Add series URLs to `links.txt`, then uncomment the volume line in `docker-compose.yaml`:

```yaml
    volumes:
      - ./data/webtoons:/data/webtoons:rw,noexec,nosuid,nodev
      - ./links.txt:/links.txt:ro,noexec,nosuid,nodev
      - /etc/localtime:/etc/localtime:ro
```

### 4. Pull the Container

```bash
docker compose -f /home/wtd/docker-compose.yaml --profile tools pull
```

### 5. Shell Alias for Interactive Use

For convenience in interactive SSH sessions, you can define a shell function in `~/.bashrc`:

```bash
nano ~/.bashrc
```

```bash
wtd() {
    docker compose -f /home/wtd/docker-compose.yaml --profile tools run --rm wtd "$@"
}
```

```bash
source ~/.bashrc
```

### 6. Scheduled Synchronization (Cron)

To run periodic synchronization, configure a cron job using the `--silent` flag to suppress interactive progress spinners and log only errors:

```bash
sudo crontab -e
```

```cron
# Run series synchronization every 6 hours (00:00, 06:00, 12:00, 18:00)
0 */6 * * * docker compose -f /home/wtd/docker-compose.yaml --profile tools run --rm -T wtd --silent >> /home/wtd/cron.log 2>&1
```

### 7. Scheduled Synchronization (Systemd Timer)

For production Linux environments, running synchronization via a hardened systemd timer and service is recommended over Cron. It provides centralized logging through `journald`, prevents overlapping runs, executes missed runs after system reboots (`Persistent=true`), and enforces strict sandboxing (`ProtectSystem=strict`, zero capabilities, isolated `/dev`).

Fetch and install the systemd unit files:

```bash
REPO="https://raw.githubusercontent.com/underhax/go-webtoons-downloader/main"

sudo curl -fsSL ${REPO}/etc/systemd/system/wtd.service -o /etc/systemd/system/wtd.service
sudo curl -fsSL ${REPO}/etc/systemd/system/wtd.timer -o /etc/systemd/system/wtd.timer
sudo chmod 0644 /etc/systemd/system/wtd.{service,timer}
sudo chown root:root /etc/systemd/system/wtd.{service,timer}
```

*(If your deployment directory differs from `/home/wtd`, adjust `WorkingDirectory` in `/etc/systemd/system/wtd.service` before starting).*

Reload systemd and enable the timer:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now wtd.timer
```

Verify the timer status and scheduled execution:

```bash
systemctl list-timers wtd.timer
```

To run an on-demand synchronization or inspect output logs:

```bash
# Trigger immediate synchronization
sudo systemctl start wtd.service

# Inspect service logs
journalctl -u wtd.service -e
```

</details>

---

<details>
<summary><strong>Usage</strong></summary>

## Usage

`wtd` operates in two primary modes: single series download or batch synchronization from a links file.

```text
Usage:
  wtd --url '<WEBTOON_URL>' [--start <NUMBER>] [GLOBAL OPTIONS]
  wtd --links <PATH> [--index <NUMBER> [--start <NUMBER>]] [GLOBAL OPTIONS]
  wtd --update
  wtd --version
  wtd --help

Target Selection:
  -u, --url '<WEBTOON_URL>'         Download a single webtoon series directly (env: WEBTOONS_URL)
  -l, --links <PATH>                Synchronize series from a links file (env: WEBTOONS_LINKS, default: ./links.txt)

Dependent Options:
  -i, --index <NUMBER>              Series line number in links.txt (skipping comments/empty lines) (requires --links) (env: WEBTOONS_INDEX)
  -s, --start <NUMBER>              Start chapter number (requires --url or --index) (env: WEBTOONS_START)

Global Options (apply to both --url and --links):
  -d, --dir <PATH>                  Target storage directory (env: WEBTOONS_DIR, default: ./data/webtoons)
  -f, --format <pdf|cbz|zip|images> Output format (env: WEBTOONS_FORMAT, default: pdf)
      --naming <slug|title>         Folder naming style: URL slug or official title (env: WEBTOONS_NAMING, default: slug)
  -c, --concurrency <NUMBER>        Concurrent page downloads (1-200) (env: WEBTOONS_CONCURRENCY, default: 120)
      --delay <DURATION<ms|s|m>>    Request delay to avoid rate limiting (default: 0s) (env: WEBTOONS_DELAY)
      --timeout <DURATION<ms|s|m>>  Network request timeout (default: 30s) (env: WEBTOONS_TIMEOUT)
  -q, --silent                      Silent mode, suppress progress output (env: WEBTOONS_SILENT)
      --series-json                 Generate series.json metadata file (always created for images format) (env: WEBTOONS_SERIES_JSON)

Other Options:
      --update                      Update executable to latest GitHub release
  -v, --version                     Show application version
  -h, --help                        Show this help message
```

### Supported Series URLs & Languages

`wtd` accepts series URLs across all official domains (`webtoons.com`, `www.webtoons.com`, `m.webtoons.com`):

```text
https://[www|m.]webtoons.com/<language>/<genre>/<series-slug>/list?title_no=<ID>
```

Supported language locales:
- `en` (English)
- `zh-hant` (Traditional Chinese)
- `th` (Thai)
- `id` (Indonesian)
- `es` (Spanish)
- `fr` (French)
- `de` (German)

### 1. Download a Single Series (`--url`)

Open the target webtoon series (title) in your browser, copy its URL from the address bar, and replace `<WEBTOON_URL>` with it:

```bash
wtd --url '<WEBTOON_URL>' [--start <NUMBER>] [GLOBAL OPTIONS]
```

**Examples:**

Download an entire series as PDF files into the default directory (`./data/webtoons/<series-slug>/`):
```bash
wtd --url '<WEBTOON_URL>'
```

Download as CBZ archives starting from chapter 100 into a custom directory (creates `/Downloads/<series-slug>/` and saves archives inside):
```bash
wtd --url '<WEBTOON_URL>' --format cbz --start 100 --dir '/Downloads'
```

Download raw images named by the official series title with a 200ms request delay (without `--dir`, files are saved to `./data/webtoons/<series-title>/` by default):
```bash
wtd --url '<WEBTOON_URL>' --format images --naming title --delay 200ms
```

### 2. Batch Synchronization (`--links`)

Synchronizes series listed in a links file. For each entry, `wtd` inspects the target directory on disk to determine the highest existing chapter number:
- If the directory is absent or empty, download starts at chapter 1.
- If chapters exist, download resumes from `last_chapter + 1`.
- If all chapters are present, the series is skipped as up-to-date.

1. Create a `links.txt` file (one URL per line; empty lines, `#` comments, and invalid URLs are automatically ignored):

```text
<WEBTOON_URL_1>

# comment
<WEBTOON_URL_2>
<WEBTOON_URL_3>
```

Series are automatically numbered sequentially starting from 1 (skipping empty lines, comments, and invalid URLs), and each series is stored in a folder prefixed with its zero-padded index:
- `<WEBTOON_URL_1>` corresponds to index `1` (`--index 1`, saved to `001. <series-slug>/`).
- `<WEBTOON_URL_2>` corresponds to index `2` (`--index 2`, saved to `002. <series-slug>/`).
- `<WEBTOON_URL_3>` corresponds to index `3` (`--index 3`, saved to `003. <series-slug>/`).

2. Run synchronization:

```bash
wtd --links ./links.txt
```

*(When `links.txt` exists in the current working directory or is mounted in Docker Compose, passing `--links` is optional: `wtd`).*

**Sync a specific entry from the list:**

Download only the 2nd series from `links.txt`:
```bash
wtd --links ./links.txt --index 2
```

Download the 2nd series starting from chapter 50:
```bash
wtd --links ./links.txt --index 2 --start 50
```

### 3. Self-Update (`--update`)

Fetch and install the latest release directly from GitHub:

```bash
wtd --update
```

</details>

---

<details>
<summary><strong>Development</strong></summary>

For local image building, static analysis, unit testing, and CI/CD pipeline documentation, see **[DEVELOPMENT.md](DEVELOPMENT.md)**.

</details>
