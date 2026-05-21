#!/usr/bin/env bash
# snap.sh — takes HTML files (or URLs) and produces PNG screenshots via chromerpc.
#
# Usage:
#   ./snap.sh <input.html> <output.png>        # single HTML file
#   ./snap.sh <input_dir/> <output_dir/>       # directory of HTML files → one PNG each
#   ./snap.sh <https://example.com> <out.png>  # external URL directly

set -euo pipefail

CHROMERPC_PORT="${CHROMERPC_PORT:-}"  # empty = auto-assign a free port

INPUT="${1:-}"
OUTPUT="${2:-}"

if [[ -z "$INPUT" || -z "$OUTPUT" ]]; then
  echo "Usage:"
  echo "  $0 <input.html> <output.png>"
  echo "  $0 <input_dir/> <output_dir/>"
  echo "  $0 <https://url> <output.png>"
  exit 1
fi

# ── Cleanup ──────────────────────────────────────────────────────────────────

WORK_DIR=""
CHROMERPC_PID=""
HTTP_PID=""
HTTP_PORT=""
CHROMERPC_ADDR=""

cleanup() {
  [[ -n "$CHROMERPC_PID" ]] && kill "$CHROMERPC_PID" 2>/dev/null || true
  [[ -n "$HTTP_PID" ]]      && kill "$HTTP_PID"      2>/dev/null || true
  [[ -n "$WORK_DIR" ]]      && rm -rf "$WORK_DIR"
}
trap cleanup EXIT

# ── Chrome detection ──────────────────────────────────────────────────────────

find_chrome() {
  if command -v google-chrome &>/dev/null;       then command -v google-chrome; return; fi
  if command -v chromium-browser &>/dev/null;    then command -v chromium-browser; return; fi
  if command -v chromium &>/dev/null;            then command -v chromium; return; fi
  local mac_chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
  if [[ -x "$mac_chrome" ]]; then echo "$mac_chrome"; return; fi
  echo ""
}

CHROME="$(find_chrome)"
if [[ -z "$CHROME" ]]; then
  echo "ERROR: Chrome not found. Install Google Chrome and try again." >&2
  exit 1
fi

# ── chromerpc setup ───────────────────────────────────────────────────────────
# Always fetched as an external tool into /tmp/chromerpc-testing.
# Cached across runs; re-cloned if missing.

CACHE="/tmp/chromerpc-testing"
CHROMERPC_BIN="$CACHE/bin/chromerpc"
AUTOMATE_BIN="$CACHE/bin/automate"

setup_chromerpc() {
  if [[ ! -x "$CHROMERPC_BIN" ]]; then
    echo "Fetching chromerpc..."
    mkdir -p "$CACHE"
    if [[ -d "$CACHE/src/.git" ]]; then
      git -C "$CACHE/src" pull --quiet
    else
      git clone --quiet https://github.com/accretional/chromerpc "$CACHE/src"
    fi
    mkdir -p "$CACHE/bin"
    cd "$CACHE/src"
    go build -o "$CHROMERPC_BIN" ./cmd/chromerpc
    go build -o "$AUTOMATE_BIN"  ./cmd/automate
    echo "chromerpc ready."
  else
    echo "Using cached chromerpc: $CHROMERPC_BIN"
  fi
}

setup_chromerpc

# ── Determine mode ────────────────────────────────────────────────────────────

is_url=false
[[ "$INPUT" == http://* || "$INPUT" == https://* ]] && is_url=true

is_dir=false
[[ -d "$INPUT" ]] && is_dir=true

# ── Free port helper ──────────────────────────────────────────────────────────

find_free_port() {
  python3 -c "import socket; s=socket.socket(); s.bind(('',0)); p=s.getsockname()[1]; s.close(); print(p)"
}

# ── Start local HTTP server (only for local HTML inputs) ──────────────────────

HTML_SERVE_DIR=""

if [[ "$is_url" == false ]]; then
  if [[ "$is_dir" == true ]]; then
    HTML_SERVE_DIR="$(cd "$INPUT" && pwd)"
  else
    HTML_SERVE_DIR="$(cd "$(dirname "$INPUT")" && pwd)"
  fi

  HTTP_PORT="$(find_free_port)"
  echo "Starting HTTP server on port $HTTP_PORT (serving $HTML_SERVE_DIR)..."
  python3 -m http.server "$HTTP_PORT" --directory "$HTML_SERVE_DIR" &>/dev/null &
  HTTP_PID=$!
  disown "$HTTP_PID"
fi

# ── Start chromerpc ───────────────────────────────────────────────────────────

# Always use a fresh free port so we never collide with stale servers
CHROMERPC_PORT="$(find_free_port)"
CHROMERPC_ADDR="localhost:$CHROMERPC_PORT"

echo "Starting chromerpc on :$CHROMERPC_PORT ..."
"$CHROMERPC_BIN" --headless --addr ":$CHROMERPC_PORT" &>/dev/null &
CHROMERPC_PID=$!
disown "$CHROMERPC_PID"

echo "Waiting for chromerpc to be ready..."
for i in $(seq 1 40); do
  if bash -c ">/dev/tcp/localhost/$CHROMERPC_PORT" 2>/dev/null; then
    echo "chromerpc is ready."
    break
  fi
  sleep 0.5
  if [[ $i -eq 40 ]]; then
    echo "ERROR: chromerpc did not become ready in time." >&2
    exit 1
  fi
done

# ── Screenshot function ───────────────────────────────────────────────────────

WORK_DIR="$(mktemp -d)"

take_screenshot() {
  local url="$1"
  local out_file="$2"

  # Ensure output directory exists and resolve absolute path
  mkdir -p "$(dirname "$out_file")"
  local abs_out
  abs_out="$(cd "$(dirname "$out_file")" && pwd)/$(basename "$out_file")"

  local slug
  slug="$(basename "$out_file" .png | tr ' /' '_-')"
  local textproto="$WORK_DIR/${slug}.textproto"

  cat > "$textproto" <<PROTO
name: "screenshot_${slug}"
steps: {
  label: "set_viewport"
  set_viewport: {
    width: 1280
    height: 800
    device_scale_factor: 2
  }
}
steps: {
  label: "navigate"
  navigate: {
    url: "$url"
  }
}
steps: {
  label: "wait_for_render"
  wait: {
    milliseconds: 500
  }
}
steps: {
  label: "capture"
  screenshot: {
    output_path: "$abs_out"
    format: "png"
  }
}
PROTO

  echo "Screenshotting: $url -> $abs_out"
  "$AUTOMATE_BIN" -addr "$CHROMERPC_ADDR" -input "$textproto"
}

# ── Run ───────────────────────────────────────────────────────────────────────

if [[ "$is_url" == true ]]; then
  # Direct URL mode
  take_screenshot "$INPUT" "$OUTPUT"

elif [[ "$is_dir" == true ]]; then
  # Directory mode: one PNG per HTML file
  mkdir -p "$OUTPUT"
  shopt -s nullglob
  html_files=("$INPUT"/*.html)
  if [[ ${#html_files[@]} -eq 0 ]]; then
    echo "ERROR: No .html files found in $INPUT" >&2
    exit 1
  fi
  for html in "${html_files[@]}"; do
    filename="$(basename "$html")"
    slug="${filename%.html}"
    url="http://localhost:$HTTP_PORT/$filename"
    outfile="$OUTPUT/${slug}.png"
    take_screenshot "$url" "$outfile"
  done

else
  # Single HTML file mode
  filename="$(basename "$INPUT")"
  url="http://localhost:$HTTP_PORT/$filename"
  take_screenshot "$url" "$OUTPUT"
fi

echo ""
echo "Done. Screenshots are ready."
